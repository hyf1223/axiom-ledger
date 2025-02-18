package txpool

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/btree"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"

	"github.com/axiomesh/axiom-kit/txpool"

	"github.com/axiomesh/axiom-ledger/internal/components/timer"

	"github.com/axiomesh/axiom-kit/types"
)

var _ txpool.TxPool[types.Transaction, *types.Transaction] = (*txPoolImpl[types.Transaction, *types.Transaction])(nil)

var (
	ErrTxPoolFull   = errors.New("tx pool full")
	ErrNonceTooLow  = errors.New("nonce too low")
	ErrNonceTooHigh = errors.New("nonce too high")
	ErrDuplicateTx  = errors.New("duplicate tx")
)

// txPoolImpl contains all currently known transactions.
type txPoolImpl[T any, Constraint types.TXConstraint[T]] struct {
	logger                logrus.FieldLogger
	selfID                uint64
	batchSize             uint64
	isTimed               bool
	txStore               *transactionStore[T, Constraint] // store all transaction info
	toleranceNonceGap     uint64
	toleranceTime         time.Duration
	toleranceRemoveTime   time.Duration
	cleanEmptyAccountTime time.Duration
	poolMaxSize           uint64

	getAccountNonce       GetAccountNonceFunc
	notifyGenerateBatch   bool
	notifyGenerateBatchFn func(typ int)
	notifyFindNextBatchFn func(completionMissingBatchHashes ...string) // notify consensus that it can find next batch

	timerMgr  timer.Timer
	statusMgr *PoolStatusMgr

	recvCh chan txPoolEvent
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (p *txPoolImpl[T, Constraint]) Start() error {
	go p.listenEvent()

	err := p.timerMgr.StartTimer(timer.RemoveTx)
	if err != nil {
		return err
	}
	err = p.timerMgr.StartTimer(timer.CleanEmptyAccount)
	if err != nil {
		return err
	}
	p.logger.Info("txpool started")
	return nil
}

func (p *txPoolImpl[T, Constraint]) listenEvent() {
	p.wg.Add(1)
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			p.logger.Info("txpool stopped")
			return
		case next := <-p.recvCh:
			for {
				select {
				case <-p.ctx.Done():
					p.logger.Info("txpool stopped")
					return
				default:
				}
				nexts := p.processEvent(next)
				if len(nexts) == 0 {
					break
				}
				next = nexts[0]
				nexts = nexts[1:]
			}
		}
	}
}

func (p *txPoolImpl[T, Constraint]) processEvent(event txPoolEvent) []txPoolEvent {
	switch e := event.(type) {
	case *addTxsEvent:
		return p.dispatchAddTxsEvent(e)
	case *removeTxsEvent:
		p.dispatchRemoveTxsEvent(e)
		return nil
	case *batchEvent:
		p.dispatchBatchEvent(e)
		return nil
	case *poolInfoEvent:
		p.dispatchGetPoolInfoEvent(e)
		return nil
	case *consensusEvent:
		p.dispatchConsensusEvent(e)
		return nil
	case *localEvent:
		p.dispatchLocalEvent(e)
	default:
		p.logger.Warning("unknown event type", e)
	}

	return nil
}

func (p *txPoolImpl[T, Constraint]) dispatchAddTxsEvent(event *addTxsEvent) []txPoolEvent {
	p.logger.Debugf("start dispatch add txs event:%s", addTxsEventToStr[event.EventType])
	var (
		fullErr                      error
		err                          error
		notifyGenBatch               bool
		completionMissingBatchHashes []string
		removeEvent                  *removeTxsEvent
		nextEvents                   []txPoolEvent
	)
	if p.statusMgr.In(PoolFull) {
		fullErr = ErrTxPoolFull
		traceRejectTx(ErrTxPoolFull.Error())
	}
	start := time.Now()
	metricsPrefix := "addTxs_"
	defer func() {
		traceProcessEvent(fmt.Sprintf("%s%s", metricsPrefix, addTxsEventToStr[event.EventType]), time.Since(start))
		p.logger.WithFields(logrus.Fields{"cost": time.Since(start)}).Debugf("end dispatch add txs event:%s", addTxsEventToStr[event.EventType])
	}()
	switch event.EventType {
	case localTxEvent:
		req := event.Event.(*reqLocalTx[T, Constraint])
		if fullErr != nil {
			req.errCh <- fullErr
			p.logger.Warning("add local tx failed", fullErr)
		} else {
			notifyGenBatch, completionMissingBatchHashes, removeEvent, err = p.addNewRequests([]*T{req.tx}, true, false, true)
			// return err for local tx
			req.errCh <- err
		}

	case remoteTxsEvent:
		if fullErr != nil {
			p.logger.Warning("add remote tx failed: ", fullErr)
		} else {
			req := event.Event.(*reqRemoteTxs[T, Constraint])
			notifyGenBatch, completionMissingBatchHashes, removeEvent, _ = p.addNewRequests(req.txs, false, false, true)
		}

	case missingTxsEvent:
		req := event.Event.(*reqMissingTxs[T, Constraint])
		if fullErr != nil {
			p.logger.Warning("add missing tx failed: ", fullErr)
			req.errCh <- fullErr
		} else {
			err = p.handleReceiveMissingRequests(req.batchHash, req.txs)
			req.errCh <- err
		}

	default:
		p.logger.Errorf("unknown addTxs event type: %d", event.EventType)
	}

	if p.checkPoolFull() {
		p.setFull()
	}

	if notifyGenBatch {
		p.logger.Infof("notify generate batch")
		p.notifyGenerateBatchFn(txpool.GenBatchSizeEvent)
	}
	if len(completionMissingBatchHashes) != 0 {
		p.logger.Infof("notify find next batch")
		p.notifyFindNextBatchFn(completionMissingBatchHashes...)
	}

	if removeEvent != nil {
		nextEvents = append(nextEvents, removeEvent)
	}
	return nextEvents
}

func (p *txPoolImpl[T, Constraint]) dispatchRemoveTxsEvent(event *removeTxsEvent) {
	p.logger.Debugf("start dispatch remove txs event:%s", removeTxsEventToStr[event.EventType])
	metricsPrefix := "removeTxs_"
	start := time.Now()
	defer func() {
		traceProcessEvent(fmt.Sprintf("%s%s", metricsPrefix, removeTxsEventToStr[event.EventType]), time.Since(start))
		p.logger.WithFields(logrus.Fields{"cost": time.Since(start)}).Debugf(
			"end dispatch remove txs event:%s", removeTxsEventToStr[event.EventType])
	}()
	var (
		removeCount int
		err         error
	)
	switch event.EventType {
	case highNonceTxsEvent:
		req := event.Event.(*reqHighNonceTxs)
		removeCount, err = p.removeTxsByAccount(req.account, req.highNonce)
		if err != nil {
			p.logger.Warningf("remove high nonce txs by account failed: %s", err)
		}
		if removeCount > 0 {
			p.logger.Warningf("successfully remove high nonce txs by account: %s, count: %d", req.account, removeCount)
			traceRemovedTx("highNonce", removeCount)
		}
	case timeoutTxsEvent:
		removeCount = p.handleRemoveTimeoutTxs()
		if removeCount > 0 {
			p.logger.Warningf("Successful remove timeout txs, count: %d", removeCount)
			traceRemovedTx("timeout", removeCount)
		}
	case committedTxsEvent:
		removeCount = p.handleRemoveStateUpdatingTxs(event.Event.(*reqRemoveCommittedTxs).txHashList)
		if removeCount > 0 {
			p.logger.Warningf("Successfully remove committed txs, count: %d", removeCount)
			traceRemovedTx("committed", removeCount)
		}
	case batchedTxsEvent:
		removeCount = p.handleRemoveBatches(event.Event.(*reqRemoveBatchedTxs).batchHashList)
		if removeCount > 0 {
			p.logger.Infof("Successfully remove batched txs, count: %d", removeCount)
			traceRemovedTx("batched", removeCount)
		}
	default:
		p.logger.Warningf("unknown removeTxs event type: %d", event.EventType)
	}
	if !p.checkPoolFull() {
		p.setNotFull()
	}
}

func (p *txPoolImpl[T, Constraint]) dispatchBatchEvent(event *batchEvent) {
	p.logger.Debugf("start dispatch batch event:%s", batchEventToStr[event.EventType])
	start := time.Now()
	metricsPrefix := "batch_"
	defer func() {
		traceProcessEvent(fmt.Sprintf("%s%s", metricsPrefix, batchEventToStr[event.EventType]), time.Since(start))
		p.logger.WithFields(logrus.Fields{"cost": time.Since(start)}).Debugf("end dispatch batch event:%d", event.EventType)
	}()
	switch event.EventType {
	case txpool.GenBatchTimeoutEvent, txpool.GenBatchFirstEvent, txpool.GenBatchSizeEvent, txpool.GenBatchNoTxTimeoutEvent:
		// it means primary receive the notify signal, and trigger the generate batch size event
		// we need reset the notify flag

		// if receive GenBatchFirstEvent, it means the new primary is elected, and it could generate batch,
		// so we need reset the notify flag in case of the new primary's txpool exist many txs
		p.notifyGenerateBatch = false

		err := p.handleGenBatchRequest(event)
		if err != nil {
			p.logger.Warning(err)
		}
	case txpool.ReConstructBatchEvent:
		req := event.Event.(*reqReConstructBatch[T, Constraint])
		deDuplicateTxHashes, err := p.handleReConstructBatchByOrder(req.oldBatch)
		if err != nil {
			req.respCh <- &respReConstructBatch{err: err}
		} else {
			req.respCh <- &respReConstructBatch{
				deDuplicateTxHashes: deDuplicateTxHashes,
			}
		}
	case txpool.GetTxsForGenBatchEvent:
		req := event.Event.(*reqGetTxsForGenBatch[T, Constraint])
		txs, localList, missingTxsHash, err := p.handleGetRequestsByHashList(req.batchHash, req.timestamp, req.hashList, req.deDuplicateTxHashes)
		if err != nil {
			req.respCh <- &respGetTxsForGenBatch[T, Constraint]{err: err}
		} else {
			resp := &respGetTxsForGenBatch[T, Constraint]{
				txs:            txs,
				localList:      localList,
				missingTxsHash: missingTxsHash,
			}
			req.respCh <- resp
		}

	default:
		p.logger.Warningf("unknown generate batch event type: %s", batchEventToStr[event.EventType])
	}
}

func (p *txPoolImpl[T, Constraint]) handleGenBatchRequest(event *batchEvent) error {
	req := event.Event.(*reqGenBatch[T, Constraint])
	batch, err := p.handleGenerateRequestBatch(event.EventType)
	respBatch := &respGenBatch[T, Constraint]{}
	if err != nil {
		respBatch.err = err
		req.respCh <- respBatch
		return err
	}
	respBatch.resp = batch
	req.respCh <- respBatch
	return nil
}

func (p *txPoolImpl[T, Constraint]) dispatchGetPoolInfoEvent(event *poolInfoEvent) {
	p.logger.Debugf("start dispatch get pool info event:%s", poolInfoEventToStr[event.EventType])
	metricsPrefix := "getInfo_"
	start := time.Now()
	defer func() {
		p.logger.WithFields(logrus.Fields{"cost": time.Since(start)}).Debugf(
			"end dispatch get pool info event:%s", poolInfoEventToStr[event.EventType])
		traceProcessEvent(fmt.Sprintf("%s%s", metricsPrefix, poolInfoEventToStr[event.EventType]), time.Since(start))
	}()
	switch event.EventType {
	case reqPendingTxCountEvent:
		req := event.Event.(*reqPendingTxCountMsg)
		req.ch <- p.handleGetTotalPendingTxCount()
	case reqNonceEvent:
		req := event.Event.(*reqNonceMsg)
		req.ch <- p.handleGetPendingTxCountByAccount(req.account)
	case reqTxEvent:
		req := event.Event.(*reqTxMsg[T, Constraint])
		req.ch <- p.handleGetPendingTxByHash(req.hash)
	case reqAccountMetaEvent:
		req := event.Event.(*reqAccountPoolMetaMsg[T, Constraint])
		req.ch <- p.handleGetAccountMeta(req.account, req.full)
	case reqPoolMetaEvent:
		req := event.Event.(*reqPoolMetaMsg[T, Constraint])
		req.ch <- p.handleGetMeta(req.full)
	}
}

func (p *txPoolImpl[T, Constraint]) dispatchConsensusEvent(event *consensusEvent) {
	p.logger.Debugf("start dispatch consensus event:%s", consensusEventToStr[event.EventType])
	start := time.Now()
	metricsPrefix := "consensus_"
	defer func() {
		traceProcessEvent(fmt.Sprintf("%s%s", metricsPrefix, consensusEventToStr[event.EventType]), time.Since(start))
		p.logger.WithFields(logrus.Fields{"cost": time.Since(start)}).Debugf(
			"end dispatch consensus event:%s", consensusEventToStr[event.EventType])
	}()

	switch event.EventType {
	case SendMissingTxsEvent:
		req := event.Event.(*reqSendMissingTxs[T, Constraint])
		txs, err := p.handleSendMissingRequests(req.batchHash, req.missingHashList)
		req.respCh <- &respSendMissingTxs[T, Constraint]{
			resp: txs,
			err:  err,
		}
	case FilterReBroadcastTxsEvent:
		req := event.Event.(*reqFilterReBroadcastTxs[T, Constraint])
		txs := p.handleFilterOutOfDateRequests(req.timeout)
		req.respCh <- txs
	case RestoreOneBatchEvent:
		req := event.Event.(*reqRestoreOneBatch)
		err := p.handleRestoreOneBatch(req.batchHash)
		req.errCh <- err
	case RestoreAllBatchedEvent:
		p.handleRestorePool()
	}
}

func (p *txPoolImpl[T, Constraint]) dispatchLocalEvent(event *localEvent) {
	p.logger.Debugf("start dispatch local event:%s", localEventToStr[event.EventType])
	start := time.Now()
	metricsPrefix := "localEvent_"
	defer func() {
		traceProcessEvent(fmt.Sprintf("%s%s", metricsPrefix, localEventToStr[event.EventType]), time.Since(start))
		p.logger.WithFields(logrus.Fields{"cost": time.Since(start)}).Debugf(
			"end dispatch local event:%s", localEventToStr[event.EventType])
	}()
	switch event.EventType {
	case gcAccountEvent:
		count := p.handleGcAccountEvent()
		if count > 0 {
			p.logger.Debugf("handle gc account event, count: %d", count)
		}
	}
}

func (p *txPoolImpl[T, Constraint]) handleGcAccountEvent() int {
	dirtyAccount := make([]string, 0)
	now := time.Now().UnixNano()
	for account, list := range p.txStore.allTxs {
		if list.checkIfGc(now, p.cleanEmptyAccountTime.Nanoseconds()) {
			dirtyAccount = append(dirtyAccount, account)
		}
	}

	lo.ForEach(dirtyAccount, func(account string, _ int) {
		p.cleanAccountInCache(account)
	})
	return len(dirtyAccount)
}

func (p *txPoolImpl[T, Constraint]) cleanAccountInCache(account string) {
	delete(p.txStore.allTxs, account)
	delete(p.txStore.nonceCache.commitNonces, account)
	delete(p.txStore.nonceCache.pendingNonces, account)
}

func (p *txPoolImpl[T, Constraint]) postEvent(event txPoolEvent) {
	p.recvCh <- event
}

func (p *txPoolImpl[T, Constraint]) AddLocalTx(tx *T) error {
	req := &reqLocalTx[T, Constraint]{
		tx:    tx,
		errCh: make(chan error),
	}

	ev := &addTxsEvent{
		EventType: localTxEvent,
		Event:     req,
	}
	p.postEvent(ev)

	return <-req.errCh
}

func (p *txPoolImpl[T, Constraint]) AddRemoteTxs(txs []*T) {
	req := &reqRemoteTxs[T, Constraint]{
		txs: txs,
	}

	ev := &addTxsEvent{
		EventType: remoteTxsEvent,
		Event:     req,
	}

	p.postEvent(ev)
}

func (p *txPoolImpl[T, Constraint]) addNewRequests(txs []*T, local, isReplace bool, needCompletionMissingBatch bool) (
	bool, []string, *removeTxsEvent, error) {

	completionMissingBatchHashes := make([]string, 0)
	validTxs := make(map[string][]*internalTransaction[T, Constraint])

	// key: missing batch hash
	// val: txHash -> bool
	matchMissingTxBatch := make(map[string]map[string]struct{})

	currentSeqNoList := make(map[string]uint64)
	duplicateTxHash := make(map[string]bool)
	duplicatePointer := make(map[txPointer]string)
	triggeredRemoveTxs := make(map[string]bool)
	for _, tx := range txs {
		txAccount := Constraint(tx).RbftGetFrom()
		txHash := Constraint(tx).RbftGetTxHash()
		txNonce := Constraint(tx).RbftGetNonce()

		// currentSeqNo means the current wanted nonce of the account
		currentSeqNo, ok := currentSeqNoList[txAccount]
		if !ok {
			currentSeqNo = p.txStore.nonceCache.getPendingNonce(txAccount)
			currentSeqNoList[txAccount] = currentSeqNo
		}

		var ready bool
		if txNonce < currentSeqNo {
			ready = true
			if !isReplace {
				// just log tx warning message from api
				if local {
					err := fmt.Errorf("%w: receive transaction [account: %s, nonce: %d, hash: %s], "+
						"but we required %d", ErrNonceTooLow, txAccount, txNonce, txHash, currentSeqNo)
					p.logger.Warningf(err.Error())
					return false, nil, nil, err
				}
				traceRejectTx(ErrNonceTooLow.Error())
				continue
			} else {
				p.logger.Warningf("Receive transaction [account: %s, nonce: %d, hash: %s], but we required %d,"+
					" will replace old tx", txAccount, txNonce, txHash, currentSeqNo)
				p.replaceTx(tx, local, ready)
				continue
			}
		}

		// reject nonce too high tx, trigger remove all high nonce txs of account
		if txNonce > currentSeqNo+p.toleranceNonceGap {
			err := fmt.Errorf("%w: receive transaction [account: %s, nonce: %d, hash: %s], "+
				"but we required %d", ErrNonceTooHigh, txAccount, txNonce, txHash, currentSeqNo)
			var removeEvent *removeTxsEvent
			if !triggeredRemoveTxs[txAccount] {
				p.logger.Warningf("%s, trigger remove all high nonce txs", err.Error())
				// trigger remove all high nonce txs
				removeEvent = &removeTxsEvent{
					EventType: highNonceTxsEvent,
					Event: &reqHighNonceTxs{
						account:   txAccount,
						highNonce: currentSeqNo,
					},
				}
			}
			traceRejectTx(ErrNonceTooHigh.Error())
			return false, nil, removeEvent, err
		}

		// ignore duplicate tx
		if pointer := p.txStore.txHashMap[txHash]; pointer != nil || duplicateTxHash[txHash] {
			err := fmt.Errorf("%w: Transaction [account: %s, nonce: %d, hash: %s] has already existed in txHashMap, "+
				"ignore it", ErrDuplicateTx, txAccount, txNonce, txHash)
			p.logger.Warning(err.Error())
			if local {
				return false, nil, nil, err
			}
			traceRejectTx(ErrDuplicateTx.Error())
			continue
		}

		// check duplicate account and nonce(but different txHash), and replace old tx
		if oldTxhash, ok := duplicatePointer[txPointer{account: txAccount, nonce: txNonce}]; ok {
			p.logger.Warningf("Receive duplicate nonce transaction [account: %s, nonce: %d, hash: %s],"+
				" will replace old tx[hash: %s]", txAccount, txNonce, txHash, oldTxhash)
			txItems := validTxs[txAccount]
			// remove old tx from txItems
			newItems := lo.Filter(txItems, func(txItem *internalTransaction[T, Constraint], index int) bool {
				return txItem.getNonce() != txNonce
			})
			validTxs[txAccount] = newItems
		}

		if p.txStore.allTxs[txAccount] != nil {
			if oldTx, ok := p.txStore.allTxs[txAccount].items[txNonce]; ok {
				p.logger.Warningf("Receive duplicate nonce transaction [account: %s, nonce: %d, hash: %s],"+
					" will replace old tx[hash: %s]", txAccount, txNonce, txHash, oldTx.getHash())
				p.replaceTx(tx, local, false)
			}
		}

		// update currentSeqNoList
		if txNonce == currentSeqNo {
			currentSeqNoList[txAccount]++
		}

		_, ok = validTxs[txAccount]
		if !ok {
			validTxs[txAccount] = make([]*internalTransaction[T, Constraint], 0)
		}
		now := time.Now().UnixNano()
		txItem := &internalTransaction[T, Constraint]{
			rawTx:       tx,
			local:       local,
			lifeTime:    Constraint(tx).RbftGetTimeStamp(),
			arrivedTime: now,
		}
		validTxs[txAccount] = append(validTxs[txAccount], txItem)
		duplicateTxHash[txHash] = true
		duplicatePointer[txPointer{account: txAccount, nonce: txNonce}] = txHash

		// for replica, add validTxs to nonBatchedTxs and check if the missing transactions and batches are fetched
		if needCompletionMissingBatch {
			for batchHash, missingTxsHash := range p.txStore.missingBatch {
				if _, ok := matchMissingTxBatch[batchHash]; !ok {
					matchMissingTxBatch[batchHash] = make(map[string]struct{}, 0)
				}
				for _, missingTxHash := range missingTxsHash {
					// we have received a tx which we are fetching.
					if txHash == missingTxHash {
						matchMissingTxBatch[batchHash][txHash] = struct{}{}
					}
				}

				// we receive all missing txs
				if len(missingTxsHash) == len(matchMissingTxBatch[batchHash]) {
					delete(p.txStore.missingBatch, batchHash)
					completionMissingBatchHashes = append(completionMissingBatchHashes, batchHash)
				}
			}
		}
	}

	// Process all the new transaction and merge any errors into the original slice
	dirtyAccounts := p.txStore.insertTxs(validTxs, local)
	// send tx to txpool store
	p.processDirtyAccount(dirtyAccounts)

	var notifyGenBatch bool

	// when primary generate batch, reset notifyGenerateBatch flag
	if p.txStore.priorityNonBatchSize >= p.batchSize && !p.notifyGenerateBatch {
		notifyGenBatch = true
		p.notifyGenerateBatch = true
	}
	return notifyGenBatch, completionMissingBatchHashes, nil, nil
}

func (p *txPoolImpl[T, Constraint]) handleRemoveTimeoutTxs() int {
	now := time.Now().UnixNano()
	removedTxs := make(map[string][]*internalTransaction[T, Constraint])
	var (
		count          int
		index          int
		revertAccounts = make(map[string]uint64)
	)
	p.txStore.removeTTLIndex.data.Ascend(func(a btree.Item) bool {
		index++
		removeKey := a.(*orderedIndexKey)
		poolTx := p.txStore.getPoolTxByTxnPointer(removeKey.account, removeKey.nonce)
		if poolTx == nil {
			p.logger.Errorf("Get nil poolTx from txStore:[account:%s, nonce:%d]", removeKey.account, removeKey.nonce)
			return true
		}
		if now-poolTx.arrivedTime > p.toleranceRemoveTime.Nanoseconds() {
			// for those batched txs, we don't need to removedTxs temporarily.
			pointer := &txPointer{account: removeKey.account, nonce: removeKey.nonce}
			if _, ok := p.txStore.batchedTxs[*pointer]; ok {
				p.logger.Debugf("find tx[account: %s, nonce:%d] from batchedTxs, ignore remove request",
					removeKey.account, removeKey.nonce)
				return true
			}

			orderedKey := &orderedIndexKey{time: poolTx.getRawTimestamp(), account: poolTx.getAccount(), nonce: poolTx.getNonce()}

			deleteTx := func(index *btreeIndex[T, Constraint]) bool {
				if tx := index.data.Get(orderedKey); tx != nil {
					p.fillRemoveTxs(orderedKey, poolTx, removedTxs)
					count++
					// remove txHashMap
					txHash := poolTx.getHash()
					p.txStore.deletePoolTx(txHash)
					return true
				}
				return false
			}

			// if remove ready txs, we need revert pending nonce
			if deleteTx(p.txStore.priorityIndex) {
				p.revertPendingNonce(pointer, revertAccounts)
				return true
			} else {
				return deleteTx(p.txStore.parkingLotIndex)
			}
		}
		return true
	})
	for account, txs := range removedTxs {
		if list, ok := p.txStore.allTxs[account]; ok {
			// remove index from removedTxs
			_ = p.cleanTxsByAccount(account, list, txs)
		}
	}

	for account, pendingNonce := range revertAccounts {
		p.logger.Debugf("Account %s revert its pendingNonce to %d", account, pendingNonce)
	}

	// when remove priorityIndex, we need to decrease priorityNonBatchSize
	// NOTICE!!! we remove priorityIndex when it's timeout, it may cause the priorityNonBatchSize is not correct.
	// for example, if we have 10 txs in priorityIndex whitch nonce is 1-10, and we remove nonce 1-5, then the priorityNonBatchSize is 5.
	// but the nonce 6-10 is not ready because we remove nonce 1-5, so the priorityNonBatchSize is actual 0.
	readyNum := uint64(p.txStore.priorityIndex.size())
	// set priorityNonBatchSize to min(nonBatchedTxs, readyNum),
	if p.txStore.priorityNonBatchSize > readyNum {
		p.logger.Debugf("Set priorityNonBatchSize from %d to the length of priorityIndex %d", p.txStore.priorityNonBatchSize, readyNum)
		p.setPriorityNonBatchSize(readyNum)
	}

	return count
}

// GetUncommittedTransactions returns the uncommitted transactions.
// not used
func (p *txPoolImpl[T, Constraint]) GetUncommittedTransactions(maxsize uint64) []*T {
	return []*T{}
}

// Stop stop metrics
// TODO(lrx): add metrics
func (p *txPoolImpl[T, Constraint]) Stop() {
	p.cancel()
	p.wg.Wait()
	close(p.recvCh)
	p.logger.Infof("TxPool stopped!!!")
}

// newTxPoolImpl returns the txpool instance.
func newTxPoolImpl[T any, Constraint types.TXConstraint[T]](config Config) (*txPoolImpl[T, Constraint], error) {
	ctx, cancel := context.WithCancel(context.Background())

	txpoolImp := &txPoolImpl[T, Constraint]{
		logger:          config.Logger,
		getAccountNonce: config.GetAccountNonce,
		isTimed:         config.IsTimed,
		recvCh:          make(chan txPoolEvent, maxChanSize),

		statusMgr: newPoolStatusMgr(),

		ctx:    ctx,
		cancel: cancel,
	}

	txpoolImp.txStore = newTransactionStore[T, Constraint](config.GetAccountNonce, config.Logger)
	if config.BatchSize == 0 {
		txpoolImp.batchSize = DefaultBatchSize
	} else {
		txpoolImp.batchSize = config.BatchSize
	}
	if config.PoolSize == 0 {
		txpoolImp.poolMaxSize = DefaultPoolSize
	} else {
		txpoolImp.poolMaxSize = config.PoolSize
	}
	if config.ToleranceTime == 0 {
		txpoolImp.toleranceTime = DefaultToleranceTime
	} else {
		txpoolImp.toleranceTime = config.ToleranceTime
	}
	if config.ToleranceRemoveTime == 0 {
		txpoolImp.toleranceRemoveTime = DefaultToleranceRemoveTime
	} else {
		txpoolImp.toleranceRemoveTime = config.ToleranceRemoveTime
	}
	if config.CleanEmptyAccountTime == 0 {
		txpoolImp.cleanEmptyAccountTime = DefaultCleanEmptyAccountTime
	} else {
		txpoolImp.cleanEmptyAccountTime = config.CleanEmptyAccountTime
	}
	if config.ToleranceNonceGap == 0 {
		txpoolImp.toleranceNonceGap = DefaultToleranceNonceGap
	} else {
		txpoolImp.toleranceNonceGap = config.ToleranceNonceGap
	}

	// init timer for remove tx
	txpoolImp.timerMgr = timer.NewTimerManager(txpoolImp.logger)
	err := txpoolImp.timerMgr.CreateTimer(timer.RemoveTx, txpoolImp.toleranceRemoveTime, txpoolImp.handleRemoveTimeout)
	if err != nil {
		return nil, err
	}
	err = txpoolImp.timerMgr.CreateTimer(timer.CleanEmptyAccount, txpoolImp.cleanEmptyAccountTime, txpoolImp.handleRemoveTimeout)
	if err != nil {
		return nil, err
	}

	txpoolImp.logger.Infof("TxPool pool size = %d", txpoolImp.poolMaxSize)
	txpoolImp.logger.Infof("TxPool batch size = %d", txpoolImp.batchSize)
	txpoolImp.logger.Infof("TxPool batch mem limit = %v", config.BatchMemLimit)
	txpoolImp.logger.Infof("TxPool batch max mem size = %d", config.BatchMaxMem)
	txpoolImp.logger.Infof("TxPool tolerance time = %v", config.ToleranceTime)
	txpoolImp.logger.Infof("TxPool tolerance remove time = %v", txpoolImp.toleranceRemoveTime)
	txpoolImp.logger.Infof("TxPool tolerance nonce gap = %d", txpoolImp.toleranceNonceGap)
	txpoolImp.logger.Infof("TxPool clean empty account time = %v", txpoolImp.cleanEmptyAccountTime)
	return txpoolImp, nil
}

func (p *txPoolImpl[T, Constraint]) Init(conf txpool.ConsensusConfig) {
	p.selfID = conf.SelfID
	p.notifyGenerateBatchFn = conf.NotifyGenerateBatchFn
	p.notifyFindNextBatchFn = conf.NotifyFindNextBatchFn
}

// GenerateRequestBatch generates a transaction batch and post it
// to outside if there are transactions in txPool.
func (p *txPoolImpl[T, Constraint]) GenerateRequestBatch(typ int) (*txpool.RequestHashBatch[T, Constraint], error) {
	return p.generateRequestBatch(typ)
}

// GenerateRequestBatch generates a transaction batch and post it
// to outside if there are transactions in txPool.
func (p *txPoolImpl[T, Constraint]) generateRequestBatch(typ int) (*txpool.RequestHashBatch[T, Constraint], error) {
	req := &reqGenBatch[T, Constraint]{
		respCh: make(chan *respGenBatch[T, Constraint]),
	}
	if typ != txpool.GenBatchSizeEvent && typ != txpool.GenBatchTimeoutEvent &&
		typ != txpool.GenBatchNoTxTimeoutEvent && typ != txpool.GenBatchFirstEvent {
		err := fmt.Errorf("invalid batch type %d", typ)
		return nil, err
	}
	ev := &batchEvent{
		EventType: typ,
		Event:     req,
	}
	p.postEvent(ev)

	resp := <-req.respCh
	if resp.err != nil {
		return nil, resp.err
	}

	return resp.resp, nil
}

// handleGenerateRequestBatch fetches next block of transactions for consensus,
// batchedTx are all txs sent to consensus but were not committed yet, txpool should filter out such txs.
func (p *txPoolImpl[T, Constraint]) handleGenerateRequestBatch(typ int) (*txpool.RequestHashBatch[T, Constraint], error) {
	switch typ {
	case txpool.GenBatchSizeEvent, txpool.GenBatchFirstEvent:
		if p.txStore.priorityNonBatchSize < p.batchSize {
			return nil, fmt.Errorf("actual batch size %d is smaller than %d, ignore generate batch",
				p.txStore.priorityNonBatchSize, p.batchSize)
		}
	case txpool.GenBatchTimeoutEvent:
		if !p.hasPendingRequestInPool() {
			return nil, fmt.Errorf("there is no pending tx, ignore generate batch")

		}
	case txpool.GenBatchNoTxTimeoutEvent:
		if p.hasPendingRequestInPool() {
			return nil, fmt.Errorf("there is pending tx, ignore generate no tx batch")

		}
		if !p.isTimed {
			err := fmt.Errorf("not supported generate no tx batch")
			p.logger.Warning(err)
			return nil, err
		}
	}

	result := make([]txPointer, 0, p.batchSize)
	// txs has lower nonce will be observed first in priority index iterator.
	p.logger.Debugf("Length of non-batched transactions: %d", p.txStore.priorityNonBatchSize)
	var batchSize uint64
	if p.txStore.priorityNonBatchSize > p.batchSize {
		batchSize = p.batchSize
	} else {
		batchSize = p.txStore.priorityNonBatchSize
	}
	skippedTxs := make(map[txPointer]bool)
	p.txStore.priorityIndex.data.Ascend(func(a btree.Item) bool {
		tx := a.(*orderedIndexKey)
		// if tx has existed in bathedTxs
		// TODO (YH): refactor batchedTxs to seen (all the transactions that have been executed) to track all txs batched in ledger.
		if _, ok := p.txStore.batchedTxs[txPointer{account: tx.account, nonce: tx.nonce}]; ok {
			return true
		}
		txSeq := tx.nonce
		// p.logger.Debugf("txpool txNonce:%s-%d", tx.account, tx.nonce)
		commitNonce := p.txStore.nonceCache.getCommitNonce(tx.account)
		// p.logger.Debugf("ledger txNonce:%s-%d", tx.account, commitNonce)
		var seenPrevious bool
		if txSeq >= 1 {
			_, seenPrevious = p.txStore.batchedTxs[txPointer{account: tx.account, nonce: txSeq - 1}]
		}
		// include transaction if it's "next" for given account or
		// we've already sent its ancestor to Consensus
		ptr := txPointer{account: tx.account, nonce: tx.nonce}
		// commitNonce is the nonce of last committed tx for given account,
		// todo(lrx): not sure if txSeq == commitNonce is correct, maybe txSeq == commitNonce+1 is correct
		if seenPrevious || (txSeq == commitNonce) {
			p.txStore.batchedTxs[ptr] = true
			result = append(result, ptr)
			if uint64(len(result)) == batchSize {
				return false
			}

			// check if we can now include some txs that were skipped before for given account
			skippedTxn := txPointer{account: tx.account, nonce: tx.nonce + 1}
			for {
				if _, ok := skippedTxs[skippedTxn]; !ok {
					break
				}
				p.txStore.batchedTxs[skippedTxn] = true
				result = append(result, skippedTxn)
				if uint64(len(result)) == batchSize {
					return false
				}
				skippedTxn.nonce++
			}
		} else {
			skippedTxs[ptr] = true
		}
		return true
	})

	if !p.isTimed && len(result) == 0 && p.hasPendingRequestInPool() {
		err := fmt.Errorf("===== Note!!! Primary generate a batch with 0 txs, "+
			"but PriorityNonBatchSize is %d, we need reset PriorityNonBatchSize", p.txStore.priorityNonBatchSize)
		p.logger.Warning(err.Error())
		p.setPriorityNonBatchSize(0)
		return nil, err
	}

	// convert transaction pointers to real values
	hashList := make([]string, len(result))
	localList := make([]bool, len(result))
	txList := make([]*T, len(result))
	for i, v := range result {
		poolTx := p.txStore.getPoolTxByTxnPointer(v.account, v.nonce)
		if poolTx == nil {
			return nil, errors.New("get nil poolTx from txStore")
		}
		hashList[i] = poolTx.getHash()

		txList[i] = poolTx.rawTx
		localList[i] = poolTx.local
	}

	txBatch := &txpool.RequestHashBatch[T, Constraint]{
		TxHashList: hashList,
		TxList:     txList,
		LocalList:  localList,
		Timestamp:  time.Now().UnixNano(),
	}
	batchHash := getBatchHash[T, Constraint](txBatch)
	txBatch.BatchHash = batchHash
	p.txStore.batchesCache[batchHash] = txBatch
	if p.txStore.priorityNonBatchSize <= uint64(len(hashList)) {
		p.setPriorityNonBatchSize(0)
	} else {
		p.decreasePriorityNonBatchSize(uint64(len(hashList)))
	}
	p.logger.Debugf("Primary generate a batch with %d txs, which hash is %s, and now there are %d "+
		"pending txs and %d batches in txPool", len(hashList), batchHash, p.txStore.priorityNonBatchSize, len(p.txStore.batchesCache))
	return txBatch, nil
}

// RestoreOneBatch moves one batch from batchStore.
func (p *txPoolImpl[T, Constraint]) RestoreOneBatch(batchHash string) error {
	req := &reqRestoreOneBatch{
		batchHash: batchHash,
		errCh:     make(chan error),
	}
	ev := &consensusEvent{
		EventType: RestoreOneBatchEvent,
		Event:     req,
	}
	p.postEvent(ev)
	return <-req.errCh
}

func (p *txPoolImpl[T, Constraint]) handleRestoreOneBatch(batchHash string) error {
	batch, ok := p.txStore.batchesCache[batchHash]
	if !ok {
		return errors.New("can't find batch from batchesCache")
	}

	// remove from batchedTxs and batchStore
	for _, hash := range batch.TxHashList {
		ptr := p.txStore.txHashMap[hash]
		if ptr == nil {
			return fmt.Errorf("can't find tx from txHashMap:[txHash:%s]", hash)
		}
		// check if the given tx exist in priorityIndex
		poolTx := p.txStore.getPoolTxByTxnPointer(ptr.account, ptr.nonce)
		key := &orderedIndexKey{time: poolTx.getRawTimestamp(), account: ptr.account, nonce: ptr.nonce}
		if tx := p.txStore.priorityIndex.data.Get(key); tx == nil {
			return fmt.Errorf("can't find tx from priorityIndex:[txHash:%s]", hash)
		}
		if !p.txStore.batchedTxs[*ptr] {
			return fmt.Errorf("can't find tx from batchedTxs:[txHash:%s]", hash)
		}
		delete(p.txStore.batchedTxs, *ptr)
	}
	delete(p.txStore.batchesCache, batchHash)
	p.increasePriorityNonBatchSize(uint64(len(batch.TxHashList)))

	p.logger.Debugf("Restore one batch, which hash is %s, now there are %d non-batched txs, "+
		"%d batches in txPool", batchHash, p.txStore.priorityNonBatchSize, len(p.txStore.batchesCache))
	return nil
}

func (p *txPoolImpl[T, Constraint]) RemoveBatches(batchHashList []string) {
	req := &reqRemoveBatchedTxs{batchHashList}
	ev := &removeTxsEvent{
		EventType: batchedTxsEvent,
		Event:     req,
	}
	p.postEvent(ev)
}

// RemoveBatches removes several batches by given digests of
// transaction batches from the pool(batchedTxs).
func (p *txPoolImpl[T, Constraint]) handleRemoveBatches(batchHashList []string) int {
	// update current cached commit nonce for account
	p.logger.Debugf("RemoveBatches: batch len:%d", len(batchHashList))
	var count int
	updateAccounts := make(map[string]uint64)
	for _, batchHash := range batchHashList {
		batch, ok := p.txStore.batchesCache[batchHash]
		if !ok {
			p.logger.Debugf("Cannot find batch %s in txpool batchedCache which may have been "+
				"discard when ReConstructBatchByOrder", batchHash)
			continue
		}
		delete(p.txStore.batchesCache, batchHash)
		dirtyAccounts := make(map[string]bool)
		for _, txHash := range batch.TxHashList {
			pointer, ok := p.txStore.txHashMap[txHash]
			if !ok {
				p.logger.Warningf("Remove transaction %s failed, Can't find it from txHashMap", txHash)
				continue
			}
			p.updateNonceCache(pointer, updateAccounts)
			p.txStore.deletePoolTx(txHash)
			delete(p.txStore.batchedTxs, *pointer)
			dirtyAccounts[pointer.account] = true
			count++
		}
		// clean related txs info in cache
		for account := range dirtyAccounts {
			if err := p.cleanTxsBeforeCommitNonce(account, p.txStore.nonceCache.getCommitNonce(account)); err != nil {
				p.logger.Errorf("cleanTxsBeforeCommitNonce error: %v", err)
			}
		}
	}
	readyNum := uint64(p.txStore.priorityIndex.size())
	// set priorityNonBatchSize to min(nonBatchedTxs, readyNum),
	if p.txStore.priorityNonBatchSize > readyNum {
		p.logger.Debugf("Set priorityNonBatchSize from %d to the length of priorityIndex %d", p.txStore.priorityNonBatchSize, readyNum)
		p.setPriorityNonBatchSize(readyNum)
	}
	for account, pendingNonce := range updateAccounts {
		p.logger.Debugf("Account %s update its pendingNonce to %d by commitNonce", account, pendingNonce)
	}
	p.logger.Infof("Removes batches in txpool, and now there are %d non-batched txs, %d batches, "+
		"priority len: %d, parkingLot len: %d, batchedTx len: %d, txHashMap len: %d", p.txStore.priorityNonBatchSize,
		len(p.txStore.batchesCache), p.txStore.priorityIndex.size(), p.txStore.parkingLotIndex.size(),
		len(p.txStore.batchedTxs), len(p.txStore.txHashMap))
	return count
}

func (p *txPoolImpl[T, Constraint]) cleanTxsBeforeCommitNonce(account string, commitNonce uint64) error {
	var outErr error
	// clean related txs info in cache
	if list, ok := p.txStore.allTxs[account]; ok {
		// remove all previous seq number txs for this account.
		removedTxs := list.forward(commitNonce)
		// remove index smaller than commitNonce delete index.
		if err := p.cleanTxsByAccount(account, list, removedTxs); err != nil {
			outErr = err
		}
	}
	return outErr
}

func (p *txPoolImpl[T, Constraint]) cleanTxsByAccount(account string, list *txSortedMap[T, Constraint], removedTxs []*internalTransaction[T, Constraint]) error {
	var failed atomic.Bool
	var wg sync.WaitGroup
	wg.Add(5)
	go func(txs []*internalTransaction[T, Constraint]) {
		defer wg.Done()
		if err := list.index.removeBySortedNonceKeys(account, txs); err != nil {
			p.logger.Errorf("removeBySortedNonceKeys error: %v", err)
			failed.Store(true)
		} else {
			lo.ForEach(txs, func(tx *internalTransaction[T, Constraint], _ int) {
				delete(list.items, tx.getNonce())
			})
		}
	}(removedTxs)
	go func(txs []*internalTransaction[T, Constraint]) {
		defer wg.Done()
		if err := p.txStore.priorityIndex.removeByOrderedQueueKeys(account, txs); err != nil {
			p.logger.Errorf("remove priorityIndex error: %v", err)
			failed.Store(true)
		}
	}(removedTxs)
	go func(txs []*internalTransaction[T, Constraint]) {
		defer wg.Done()
		if err := p.txStore.parkingLotIndex.removeByOrderedQueueKeys(account, txs); err != nil {
			p.logger.Errorf("remove parkingLotIndex error: %v", err)
			failed.Store(true)
		}
	}(removedTxs)
	go func(txs []*internalTransaction[T, Constraint]) {
		defer wg.Done()
		if err := p.txStore.localTTLIndex.removeByOrderedQueueKeys(account, txs); err != nil {
			p.logger.Errorf("remove localTTLIndex error: %v", err)
			failed.Store(true)
		}
	}(removedTxs)
	go func(txs []*internalTransaction[T, Constraint]) {
		defer wg.Done()
		if err := p.txStore.removeTTLIndex.removeByOrderedQueueKeys(account, txs); err != nil {
			p.logger.Errorf("remove removeTTLIndex error: %v", err)
			failed.Store(true)
		}
	}(removedTxs)
	wg.Wait()

	if failed.Load() {
		return errors.New("failed to remove txs")
	}

	if len(list.items) == 0 {
		list.setEmpty()
	}

	return nil
}

func (p *txPoolImpl[T, Constraint]) revertPendingNonce(pointer *txPointer, updateAccounts map[string]uint64) {
	pendingNonce := p.txStore.nonceCache.getPendingNonce(pointer.account)
	// because we remove the tx from the txpool, so we need revert the pendingNonce to pointer.nonce
	// it means we want next nonce is pointer.nonce
	if pendingNonce > pointer.nonce {
		p.txStore.nonceCache.setPendingNonce(pointer.account, pointer.nonce)
		updateAccounts[pointer.account] = pointer.nonce
	}
}

func (p *txPoolImpl[T, Constraint]) updateNonceCache(pointer *txPointer, updateAccounts map[string]uint64) {
	preCommitNonce := p.txStore.nonceCache.getCommitNonce(pointer.account)
	// next wanted nonce
	newCommitNonce := pointer.nonce + 1
	if preCommitNonce < newCommitNonce {
		p.txStore.nonceCache.setCommitNonce(pointer.account, newCommitNonce)
		// Note!!! updating pendingNonce to commitNonce for the restart node
		pendingNonce := p.txStore.nonceCache.getPendingNonce(pointer.account)
		if pendingNonce < newCommitNonce {
			updateAccounts[pointer.account] = newCommitNonce
			p.txStore.nonceCache.setPendingNonce(pointer.account, newCommitNonce)
		}
	}
}

func (p *txPoolImpl[T, Constraint]) RemoveStateUpdatingTxs(txHashList []string) {
	req := &reqRemoveCommittedTxs{txHashList}
	ev := &removeTxsEvent{
		EventType: committedTxsEvent,
		Event:     req,
	}
	p.postEvent(ev)
}

func (p *txPoolImpl[T, Constraint]) handleRemoveStateUpdatingTxs(txHashList []string) int {
	p.logger.Infof("start RemoveStateUpdatingTxs, len:%d", len(txHashList))
	removeCount := 0
	dirtyAccounts := make(map[string]bool)
	updateAccounts := make(map[string]uint64)
	removeTxs := make(map[string][]*internalTransaction[T, Constraint])
	lo.ForEach(txHashList, func(txHash string, _ int) {
		if pointer, ok := p.txStore.txHashMap[txHash]; ok {
			poolTx := p.txStore.getPoolTxByTxnPointer(pointer.account, pointer.nonce)
			if poolTx == nil {
				p.logger.Errorf("pool tx %s not found in txpool, but exists in txHashMap", txHash)
				return
			}
			// update nonce when remove tx
			p.updateNonceCache(pointer, updateAccounts)
			// remove from txHashMap
			p.txStore.deletePoolTx(txHash)
			if removeTxs[pointer.account] == nil {
				removeTxs[pointer.account] = make([]*internalTransaction[T, Constraint], 0)
			}
			// record dirty accounts and removeTxs
			removeTxs[pointer.account] = append(removeTxs[pointer.account], poolTx)
			dirtyAccounts[pointer.account] = true
		}
	})

	for account := range dirtyAccounts {
		if list, ok := p.txStore.allTxs[account]; ok {
			if err := p.cleanTxsByAccount(account, list, removeTxs[account]); err != nil {
				p.logger.Errorf("cleanTxsByAccount error: %v", err)
			} else {
				removeCount += len(removeTxs[account])
			}
		}
	}

	readyNum := uint64(p.txStore.priorityIndex.size())
	// set priorityNonBatchSize to min(nonBatchedTxs, readyNum),
	if p.txStore.priorityNonBatchSize > readyNum {
		p.logger.Infof("Set priorityNonBatchSize from %d to the length of priorityIndex %d", p.txStore.priorityNonBatchSize, readyNum)
		p.setPriorityNonBatchSize(readyNum)
	}

	for account, pendingNonce := range updateAccounts {
		p.logger.Debugf("Account %s update its pendingNonce to %d by commitNonce", account, pendingNonce)
	}

	if removeCount > 0 {
		p.logger.Infof("finish RemoveStateUpdatingTxs, len:%d, removeCount:%d", len(txHashList), removeCount)
		traceRemovedTx("RemoveStateUpdatingTxs", removeCount)
	}
	return removeCount
}

// GetRequestsByHashList returns the transaction list corresponding to the given hash list.
// When replicas receive hashList from primary, they need to generate a totally same
// batch to primary generated one. deDuplicateTxHashes specifies some txs which should
// be excluded from duplicate rules.
//  1. If this batch has been batched, just return its transactions without error.
//  2. If we have checked this batch and found we were missing some transactions, just
//     return the same missingTxsHash as before without error.
//  3. If one transaction in hashList has been batched before in another batch,
//     return ErrDuplicateTx
//  4. If we miss some transactions, we need to fetch these transactions from primary,
//     and return missingTxsHash without error
//  5. If this node get all transactions from pool, generate a batch and return its
//     transactions without error
func (p *txPoolImpl[T, Constraint]) GetRequestsByHashList(batchHash string, timestamp int64, hashList []string,
	deDuplicateTxHashes []string) (txs []*T, localList []bool, missingTxsHash map[uint64]string, err error) {

	req := &reqGetTxsForGenBatch[T, Constraint]{
		batchHash:           batchHash,
		timestamp:           timestamp,
		hashList:            hashList,
		deDuplicateTxHashes: deDuplicateTxHashes,
		respCh:              make(chan *respGetTxsForGenBatch[T, Constraint]),
	}

	ev := &batchEvent{
		EventType: txpool.GetTxsForGenBatchEvent,
		Event:     req,
	}
	p.postEvent(ev)

	resp := <-req.respCh
	if resp.err != nil {
		err = resp.err
		return
	}
	txs = resp.txs
	localList = resp.localList
	missingTxsHash = resp.missingTxsHash
	return
}

func (p *txPoolImpl[T, Constraint]) handleGetRequestsByHashList(batchHash string, timestamp int64, hashList []string,
	deDuplicateTxHashes []string) ([]*T, []bool, map[uint64]string, error) {
	var (
		txs            []*T
		localList      []bool
		missingTxsHash map[uint64]string
		err            error
	)
	if batch, ok := p.txStore.batchesCache[batchHash]; ok {
		// If replica already has this batch, directly return tx list
		p.logger.Debugf("Batch %s is already in batchesCache", batchHash)
		return batch.TxList, batch.LocalList, nil, nil
	}

	// If we have checked this batch and found we miss some transactions,
	// just return the same missingTxsHash as before
	if missingBatch, ok := p.txStore.missingBatch[batchHash]; ok {
		p.logger.Debugf("GetRequestsByHashList failed, find batch %s in missingBatch store", batchHash)
		return nil, nil, missingBatch, nil
	}

	deDuplicateMap := make(map[string]bool)
	for _, duplicateHash := range deDuplicateTxHashes {
		deDuplicateMap[duplicateHash] = true
	}

	missingTxsHash = make(map[uint64]string)
	var hasMissing bool
	for index, txHash := range hashList {
		pointer := p.txStore.txHashMap[txHash]
		if pointer == nil {
			p.logger.Debugf("Can't find tx by hash: %s from txpool", txHash)
			missingTxsHash[uint64(index)] = txHash
			hasMissing = true
			continue
		}
		if deDuplicateMap[txHash] {
			// ignore deDuplicate txs for duplicate rule
			p.logger.Warningf("Ignore de-duplicate tx %s when create same batch", txHash)
		} else {
			if _, ok := p.txStore.batchedTxs[*pointer]; ok {
				// If this transaction has been batched, return ErrDuplicateTx
				p.logger.Warningf("Duplicate transaction in getTxsByHashList with "+
					"hash: %s, batch batchHash: %s", txHash, batchHash)
				err = errors.New("duplicate transaction")
				return nil, nil, nil, err
			}
		}
		poolTx := p.txStore.getPoolTxByTxnPointer(pointer.account, pointer.nonce)
		if poolTx == nil {
			p.logger.Warningf("Transaction %s exist in txHashMap but not in allTxs", txHash)
			p.txStore.deletePoolTx(txHash)
			missingTxsHash[uint64(index)] = txHash
			hasMissing = true
			continue
		}

		if !hasMissing {
			txs = append(txs, poolTx.rawTx)
			localList = append(localList, poolTx.local)
		}
	}

	if len(missingTxsHash) != 0 {
		p.txStore.missingBatch[batchHash] = missingTxsHash
		return nil, nil, missingTxsHash, nil
	}
	for _, txHash := range hashList {
		pointer := p.txStore.txHashMap[txHash]
		p.txStore.batchedTxs[*pointer] = true
	}
	// store the batch to cache
	batch := &txpool.RequestHashBatch[T, Constraint]{
		BatchHash:  batchHash,
		TxList:     txs,
		TxHashList: hashList,
		LocalList:  localList,
		Timestamp:  timestamp,
	}
	p.txStore.batchesCache[batchHash] = batch
	if p.txStore.priorityNonBatchSize <= uint64(len(hashList)) {
		p.setPriorityNonBatchSize(0)
	} else {
		p.decreasePriorityNonBatchSize(uint64(len(hashList)))
	}
	missingTxsHash = nil
	p.logger.Debugf("Replica generate a batch, which digest is %s, and now there are %d "+
		"non-batched txs and %d batches in txpool", batchHash, p.txStore.priorityNonBatchSize, len(p.txStore.batchesCache))
	return txs, localList, missingTxsHash, nil
}

func (p *txPoolImpl[T, Constraint]) HasPendingRequestInPool() bool {
	return p.hasPendingRequestInPool()
}

func (p *txPoolImpl[T, Constraint]) hasPendingRequestInPool() bool {
	return p.statusMgr.In(HasPendingRequest)
}

func (p *txPoolImpl[T, Constraint]) checkPendingRequestInPool() bool {
	return p.txStore.priorityNonBatchSize > 0
}

func (p *txPoolImpl[T, Constraint]) PendingRequestsNumberIsReady() bool {
	return p.statusMgr.In(ReadyGenerateBatch)
}

func (p *txPoolImpl[T, Constraint]) checkPendingRequestsNumberIsReady() bool {
	return p.txStore.priorityNonBatchSize >= p.batchSize
}

func (p *txPoolImpl[T, Constraint]) ReceiveMissingRequests(batchHash string, txs map[uint64]*T) error {
	req := &reqMissingTxs[T, Constraint]{
		batchHash: batchHash,
		txs:       txs,
		errCh:     make(chan error),
	}

	ev := &addTxsEvent{
		EventType: missingTxsEvent,
		Event:     req,
	}

	p.postEvent(ev)
	return <-req.errCh
}

func (p *txPoolImpl[T, Constraint]) handleReceiveMissingRequests(batchHash string, txs map[uint64]*T) error {
	p.logger.Debugf("Replica received %d missingTxs, batch hash: %s", len(txs), batchHash)
	if _, ok := p.txStore.missingBatch[batchHash]; !ok {
		p.logger.Debugf("Can't find batch %s from missingBatch", batchHash)
		return nil
	}
	expectLen := len(p.txStore.missingBatch[batchHash])
	if len(txs) != expectLen {
		return fmt.Errorf("receive unmatched fetching txn response, expect "+
			"length: %d, received length: %d", expectLen, len(txs))
	}
	validTxn := make([]*T, 0)
	targetBatch := p.txStore.missingBatch[batchHash]
	for index, tx := range txs {
		txHash := Constraint(tx).RbftGetTxHash()
		if txHash != targetBatch[index] {
			return errors.New("find a hash mismatch tx")
		}
		validTxn = append(validTxn, tx)
	}
	var _, _, _, err = p.addNewRequests(validTxn, false, true, false)
	if err != nil {
		return err
	}
	delete(p.txStore.missingBatch, batchHash)
	return nil
}

func (p *txPoolImpl[T, Constraint]) SendMissingRequests(batchHash string, missingHashList map[uint64]string) (txs map[uint64]*T, err error) {
	req := &reqSendMissingTxs[T, Constraint]{
		batchHash:       batchHash,
		missingHashList: missingHashList,
		respCh:          make(chan *respSendMissingTxs[T, Constraint]),
	}
	ev := &consensusEvent{
		EventType: SendMissingTxsEvent,
		Event:     req,
	}
	p.postEvent(ev)
	resp := <-req.respCh
	if resp.err != nil {
		return nil, resp.err
	}
	txs = resp.resp
	return
}

func (p *txPoolImpl[T, Constraint]) handleSendMissingRequests(batchHash string, missingHashList map[uint64]string) (txs map[uint64]*T, err error) {
	for _, txHash := range missingHashList {
		if pointer := p.txStore.txHashMap[txHash]; pointer == nil {
			return nil, fmt.Errorf("transaction %s doesn't exist in txHashMap", txHash)
		}
	}
	var targetBatch *txpool.RequestHashBatch[T, Constraint]
	var ok bool
	if targetBatch, ok = p.txStore.batchesCache[batchHash]; !ok {
		return nil, fmt.Errorf("batch %s doesn't exist in batchedCache", batchHash)
	}
	targetBatchLen := uint64(len(targetBatch.TxList))
	txs = make(map[uint64]*T)
	for index, txHash := range missingHashList {
		if index >= targetBatchLen || targetBatch.TxHashList[index] != txHash {
			return nil, fmt.Errorf("find invalid transaction, index: %d, targetHash: %s", index, txHash)
		}
		txs[index] = targetBatch.TxList[index]
	}
	return
}

// ReConstructBatchByOrder reconstruct batch from empty txPool by order, must be called after RestorePool.
func (p *txPoolImpl[T, Constraint]) ReConstructBatchByOrder(oldBatch *txpool.RequestHashBatch[T, Constraint]) (
	deDuplicateTxHashes []string, err error) {

	req := &reqReConstructBatch[T, Constraint]{
		oldBatch: oldBatch,
		respCh:   make(chan *respReConstructBatch),
	}
	ev := &batchEvent{
		EventType: txpool.ReConstructBatchEvent,
		Event:     req,
	}
	p.postEvent(ev)
	resp := <-req.respCh
	return resp.deDuplicateTxHashes, resp.err
}

func (p *txPoolImpl[T, Constraint]) handleReConstructBatchByOrder(oldBatch *txpool.RequestHashBatch[T, Constraint]) ([]string, error) {

	// check if there exists duplicate batch hash.
	if _, ok := p.txStore.batchesCache[oldBatch.BatchHash]; ok {
		p.logger.Warningf("When re-construct batch, batch %s already exists", oldBatch.BatchHash)
		err := errors.New("invalid batch: batch already exists")
		return nil, err
	}

	// TxHashList has to match TxList by length and content
	if len(oldBatch.TxHashList) != len(oldBatch.TxList) {
		p.logger.Warningf("Batch is invalid because TxHashList and TxList have different lengths.")
		err := errors.New("invalid batch: TxHashList and TxList have different lengths")
		return nil, err
	}

	for i, tx := range oldBatch.TxList {
		txHash := Constraint(tx).RbftGetTxHash()
		if txHash != oldBatch.TxHashList[i] {
			p.logger.Warningf("Batch is invalid because the hash %s in txHashList does not match "+
				"the calculated hash %s of the corresponding transaction.", oldBatch.TxHashList[i], txHash)
			err := errors.New("invalid batch: hash of transaction does not match")
			return nil, err
		}
	}

	localList := make([]bool, len(oldBatch.TxHashList))

	lo.ForEach(oldBatch.TxHashList, func(_ string, index int) {
		localList[index] = false
	})

	batch := &txpool.RequestHashBatch[T, Constraint]{
		TxHashList: oldBatch.TxHashList,
		TxList:     oldBatch.TxList,
		LocalList:  localList,
		Timestamp:  oldBatch.Timestamp,
	}
	// The given batch hash should match with the calculated batch hash.
	batch.BatchHash = getBatchHash[T, Constraint](batch)
	if batch.BatchHash != oldBatch.BatchHash {
		p.logger.Warningf("The given batch hash %s does not match with the "+
			"calculated batch hash %s.", oldBatch.BatchHash, batch.BatchHash)
		err := errors.New("invalid batch: batch hash does not match")
		return nil, err
	}

	deDuplicateTxHashes := make([]string, 0)

	// There may be some duplicate transactions which are batched in different batches during vc, for those txs,
	// we only accept them in the first batch containing them and de-duplicate them in following batches.
	for _, tx := range oldBatch.TxList {
		ptr := &txPointer{
			account: Constraint(tx).RbftGetFrom(),
			nonce:   Constraint(tx).RbftGetNonce(),
		}
		txHash := Constraint(tx).RbftGetTxHash()
		if _, ok := p.txStore.batchedTxs[*ptr]; ok {
			p.logger.Warningf("De-duplicate tx %s when re-construct batch by order", txHash)
			deDuplicateTxHashes = append(deDuplicateTxHashes, txHash)
		} else {
			p.txStore.batchedTxs[*ptr] = true
		}
	}
	p.logger.Debugf("ReConstructBatchByOrder batch %s into batchedCache", oldBatch.BatchHash)
	p.txStore.batchesCache[batch.BatchHash] = batch
	return deDuplicateTxHashes, nil
}

func (p *txPoolImpl[T, Constraint]) RestorePool() {
	ev := &consensusEvent{
		EventType: RestoreAllBatchedEvent,
	}
	p.postEvent(ev)
}

// RestorePool move all batched txs back to non-batched tx which should
// only be used after abnormal recovery.
func (p *txPoolImpl[T, Constraint]) handleRestorePool() {
	p.logger.Debugf("Before restore pool, there are %d non-batched txs, %d batches, "+
		"priority len: %d, parkingLot len: %d, batchedTx len: %d, txHashMap len: %d, local txs: %d", p.txStore.priorityNonBatchSize,
		len(p.txStore.batchesCache), p.txStore.priorityIndex.size(), p.txStore.parkingLotIndex.size(),
		len(p.txStore.batchedTxs), len(p.txStore.txHashMap), p.txStore.localTTLIndex.size())

	// if the batches are fetched from primary, those txs will be put back to non-batched cache.
	// rollback txs currentSeq
	var (
		account string
		nonce   uint64
	)
	// record the smallest nonce of each account
	rollbackTxsNonce := make(map[string]uint64)
	for _, batch := range p.txStore.batchesCache {
		for _, tx := range batch.TxList {
			account = Constraint(tx).RbftGetFrom()
			nonce = Constraint(tx).RbftGetNonce()

			if _, ok := rollbackTxsNonce[account]; !ok {
				rollbackTxsNonce[account] = nonce
			} else if nonce < rollbackTxsNonce[account] {
				// if the nonce is smaller than the currentSeq, update it
				rollbackTxsNonce[account] = nonce
			}
		}
		// increase priorityNonBatchSize
		p.increasePriorityNonBatchSize(uint64(len(batch.TxList)))
	}
	// rollback nonce for each account
	for acc, non := range rollbackTxsNonce {
		p.txStore.nonceCache.setPendingNonce(acc, non)
	}

	// resubmit these txs to txpool.
	for batchDigest, batch := range p.txStore.batchesCache {
		p.logger.Debugf("Put batch %s back to pool with %d txs", batchDigest, len(batch.TxList))
		lo.ForEach(batch.TxList, func(tx *T, index int) {
			_, _, _, err := p.addNewRequests([]*T{tx}, batch.LocalList[index], true, false)
			if err != nil {
				p.logger.Warningf("Put tx[account:%s, nonce:%d] back to pool failed: %v",
					Constraint(tx).RbftGetFrom(), Constraint(tx).RbftGetNonce(), err)
			}

		})
		delete(p.txStore.batchesCache, batchDigest)
	}

	// clear missingTxs after abnormal.
	p.txStore.missingBatch = make(map[string]map[uint64]string)
	p.txStore.batchedTxs = make(map[txPointer]bool)
	p.logger.Debugf("After restore pool, there are %d non-batched txs, %d batches, "+
		"priority len: %d, parkingLot len: %d, batchedTx len: %d, txHashMap len: %d, local txs: %d", p.txStore.priorityNonBatchSize,
		len(p.txStore.batchesCache), p.txStore.priorityIndex.size(), p.txStore.parkingLotIndex.size(),
		len(p.txStore.batchedTxs), len(p.txStore.txHashMap), p.txStore.localTTLIndex.size())
}

// FilterOutOfDateRequests get the remained local txs in TTLIndex and broadcast to other vp peers by tolerance time.
func (p *txPoolImpl[T, Constraint]) FilterOutOfDateRequests(timeout bool) []*T {
	req := &reqFilterReBroadcastTxs[T, Constraint]{
		timeout: timeout,
		respCh:  make(chan []*T),
	}
	ev := &consensusEvent{
		EventType: FilterReBroadcastTxsEvent,
		Event:     req,
	}
	p.postEvent(ev)
	return <-req.respCh
}

func (p *txPoolImpl[T, Constraint]) handleFilterOutOfDateRequests(timeout bool) []*T {
	now := time.Now().UnixNano()
	var forward []*internalTransaction[T, Constraint]
	p.txStore.localTTLIndex.data.Ascend(func(a btree.Item) bool {
		orderedKey := a.(*orderedIndexKey)
		poolTx := p.txStore.getPoolTxByTxnPointer(orderedKey.account, orderedKey.nonce)
		if poolTx == nil {
			p.logger.Error("Get nil poolTx from txStore")
			return true
		}

		if !timeout || now-poolTx.lifeTime > p.toleranceTime.Nanoseconds() {
			// for those batched txs, we don't need to forward temporarily.
			if _, ok := p.txStore.batchedTxs[txPointer{account: orderedKey.account, nonce: orderedKey.nonce}]; !ok {
				forward = append(forward, poolTx)
			}
		} else {
			return false
		}
		return true
	})
	result := make([]*T, len(forward))
	// update pool tx's timestamp to now for next forwarding check.
	for i, poolTx := range forward {
		// update localTTLIndex
		p.txStore.localTTLIndex.updateIndex(poolTx, now)
		result[i] = poolTx.rawTx
		// update txpool tx in allTxs
		poolTx.lifeTime = now
	}
	return result
}

func (p *txPoolImpl[T, Constraint]) fillRemoveTxs(orderedKey *orderedIndexKey, poolTx *internalTransaction[T, Constraint],
	removedTxs map[string][]*internalTransaction[T, Constraint]) {
	if _, ok := removedTxs[orderedKey.account]; !ok {
		removedTxs[orderedKey.account] = make([]*internalTransaction[T, Constraint], 0)
	}
	// record need removedTxs and the count
	removedTxs[orderedKey.account] = append(removedTxs[orderedKey.account], poolTx)
}

func (p *txPoolImpl[T, Constraint]) removeTxsByAccount(account string, nonce uint64) (int, error) {
	var removeTxs []*internalTransaction[T, Constraint]
	if list, ok := p.txStore.allTxs[account]; ok {
		removeTxs = list.behind(nonce)

		// 1. remove all txs in txHashMap
		lo.ForEach(removeTxs, func(poolTx *internalTransaction[T, Constraint], index int) {
			txHash := poolTx.getHash()
			p.txStore.deletePoolTx(txHash)
		})

		// 2. remove index from removedTxs
		if err := p.cleanTxsByAccount(account, list, removeTxs); err != nil {
			return 0, err
		}
	}

	return len(removeTxs), nil
}

// =============================================================================
// internal methods
// =============================================================================
func (p *txPoolImpl[T, Constraint]) processDirtyAccount(dirtyAccounts map[string]bool) {
	for account := range dirtyAccounts {
		if list, ok := p.txStore.allTxs[account]; ok {
			// search for related sequential txs in allTxs
			// and add these txs into priorityIndex and parkingLotIndex.
			pendingNonce := p.txStore.nonceCache.getPendingNonce(account)
			readyTxs, nonReadyTxs, nextDemandNonce := list.filterReady(pendingNonce)
			p.txStore.nonceCache.setPendingNonce(account, nextDemandNonce)

			// insert ready txs into priorityIndex
			for _, poolTx := range readyTxs {
				p.txStore.priorityIndex.insertByOrderedQueueKey(poolTx)
				// p.logger.Debugf("insert ready tx[account: %s, nonce: %d] into priorityIndex", poolTx.getAccount(), poolTx.getNonce())
			}
			p.increasePriorityNonBatchSize(uint64(len(readyTxs)))

			// insert non-ready txs into parkingLotIndex
			for _, poolTx := range nonReadyTxs {
				p.txStore.parkingLotIndex.insertByOrderedQueueKey(poolTx)
			}
		}
	}
}

func (p *txPoolImpl[T, Constraint]) replaceTx(tx *T, local, ready bool) {
	account := Constraint(tx).RbftGetFrom()
	txHash := Constraint(tx).RbftGetTxHash()
	txNonce := Constraint(tx).RbftGetNonce()
	oldPoolTx := p.txStore.getPoolTxByTxnPointer(account, txNonce)
	pointer := &txPointer{
		account: account,
		nonce:   txNonce,
	}

	// remove old tx from txHashMap、priorityIndex、parkingLotIndex、localTTLIndex and removeTTLIndex
	if oldPoolTx != nil {
		p.txStore.deletePoolTx(oldPoolTx.getHash())
		p.txStore.priorityIndex.removeByOrderedQueueKey(oldPoolTx)
		p.txStore.parkingLotIndex.removeByOrderedQueueKey(oldPoolTx)
		p.txStore.removeTTLIndex.removeByOrderedQueueKey(oldPoolTx)
		p.txStore.localTTLIndex.removeByOrderedQueueKey(oldPoolTx)
	}

	if !ready {
		// if not ready, just delete old tx, outbound will handle insert new tx
		return
	}
	// insert new tx
	p.txStore.insertPoolTx(txHash, pointer)

	// update txPointer in allTxs
	list, ok := p.txStore.allTxs[account]
	if !ok {
		list = newTxSortedMap[T, Constraint]()
		p.txStore.allTxs[account] = list
	}
	list.index.insertBySortedNonceKey(txNonce)
	now := time.Now().UnixNano()
	newPoolTx := &internalTransaction[T, Constraint]{
		local:       false,
		rawTx:       tx,
		lifeTime:    Constraint(tx).RbftGetTimeStamp(),
		arrivedTime: now,
	}
	list.items[txNonce] = newPoolTx

	// insert new tx received from remote vp
	p.txStore.priorityIndex.insertByOrderedQueueKey(newPoolTx)
	if local {
		p.txStore.localTTLIndex.insertByOrderedQueueKey(newPoolTx)
	}
	p.txStore.removeTTLIndex.insertByOrderedQueueKey(newPoolTx)
}

// getBatchHash calculate hash of a RequestHashBatch
func getBatchHash[T any, Constraint types.TXConstraint[T]](batch *txpool.RequestHashBatch[T, Constraint]) string {
	h := md5.New()
	for _, hash := range batch.TxHashList {
		_, _ = h.Write([]byte(hash))
	}
	if batch.Timestamp > 0 {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, uint64(batch.Timestamp))
		_, _ = h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (p *txPoolImpl[T, Constraint]) increasePriorityNonBatchSize(addSize uint64) {
	p.txStore.priorityNonBatchSize = p.txStore.priorityNonBatchSize + addSize
	if p.checkPendingRequestInPool() {
		p.setHasPendingRequest()
	}
	if p.checkPendingRequestsNumberIsReady() {
		p.setReady()
	}
	readyTxNum.Set(float64(p.txStore.priorityNonBatchSize))
}

func (p *txPoolImpl[T, Constraint]) decreasePriorityNonBatchSize(subSize uint64) {
	p.txStore.priorityNonBatchSize = p.txStore.priorityNonBatchSize - subSize
	if !p.checkPendingRequestInPool() {
		p.setNoPendingRequest()
	}
	if !p.checkPendingRequestsNumberIsReady() {
		p.setNotReady()
	}
	readyTxNum.Set(float64(p.txStore.priorityNonBatchSize))
}

func (p *txPoolImpl[T, Constraint]) setPriorityNonBatchSize(txnSize uint64) {
	p.txStore.priorityNonBatchSize = txnSize
	if p.checkPendingRequestInPool() {
		p.setHasPendingRequest()
	} else {
		p.setNoPendingRequest()
	}

	if p.checkPendingRequestsNumberIsReady() {
		p.setReady()
	} else {
		p.setNotReady()
	}
	readyTxNum.Set(float64(p.txStore.priorityNonBatchSize))
}
