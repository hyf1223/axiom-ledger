package solo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/axiomesh/axiom/internal/order/txcache"
	"github.com/ethereum/go-ethereum/event"
	"github.com/sirupsen/logrus"

	"github.com/axiomesh/axiom-bft/mempool"
	"github.com/axiomesh/axiom-kit/types"
	"github.com/axiomesh/axiom/internal/order"
	"github.com/axiomesh/axiom/internal/peermgr"
)

const maxChanSize = 1024

// consensusEvent is a type meant to clearly convey that the return type or parameter to a function will be supplied to/from an events.Manager
type consensusEvent any

type Node struct {
	ID               uint64
	isTimed          bool
	commitC          chan *types.CommitEvent                                               // block channel
	logger           logrus.FieldLogger                                                    // logger
	mempool          mempool.MemPool[types.Transaction, *types.Transaction]                // transaction pool
	recvCh           chan consensusEvent                                                   // receive message from consensus engine
	blockCh          chan *mempool.RequestHashBatch[types.Transaction, *types.Transaction] // receive batch from mempool
	stateC           chan *chainState
	batchMgr         *timerManager
	noTxBatchTimeout time.Duration       // generate no-tx block period
	batchTimeout     time.Duration       // generate block period
	lastExec         uint64              // the index of the last-applied block
	peerMgr          peermgr.PeerManager // network manager

	ctx    context.Context
	cancel context.CancelFunc
	sync.RWMutex
	txFeed event.Feed
}

// GetPendingTxByHash mempool is always empty in solo
func (n *Node) GetPendingTxByHash(_ *types.Hash) *types.Transaction {
	return nil
}

func (n *Node) Start() error {
	n.ctx, n.cancel = context.WithCancel(context.Background())
	n.logger.Info("consensus started")
	if n.isTimed {
		n.batchMgr.startTimer(NoTxBatch)
	}
	go n.listenEvent()
	go n.listenReadyBlock()
	return nil
}

func (n *Node) Stop() {
	n.cancel()
	n.logger.Info("consensus stopped")
}

func (n *Node) GetPendingNonceByAccount(account string) uint64 {
	return n.mempool.GetPendingNonceByAccount(account)
}

func (n *Node) DelNode(uint64) error {
	return nil
}

func (n *Node) Prepare(tx *types.Transaction) error {
	if err := n.Ready(); err != nil {
		return fmt.Errorf("node get ready failed: %w", err)
	}
	txWithResp := &txcache.TxWithResp{
		Tx: tx,
		Ch: make(chan bool),
	}
	n.recvCh <- txWithResp
	<-txWithResp.Ch
	return nil
}

func (n *Node) Commit() chan *types.CommitEvent {
	return n.commitC
}

func (n *Node) Step([]byte) error {
	return nil
}

func (n *Node) Ready() error {
	return nil
}

func (n *Node) ReportState(height uint64, blockHash *types.Hash, txHashList []*types.Hash) {
	state := &chainState{
		Height:     height,
		BlockHash:  blockHash,
		TxHashList: txHashList,
	}
	n.recvCh <- state
}

func (n *Node) Quorum() uint64 {
	return 1
}

func (n *Node) SubscribeTxEvent(events chan<- []*types.Transaction) event.Subscription {
	return n.txFeed.Subscribe(events)
}

func NewNode(opts ...order.Option) (order.Order, error) {
	config, err := order.GenerateConfig(opts...)
	if err != nil {
		return nil, fmt.Errorf("generate config: %w", err)
	}

	fn := func(addr string) uint64 {
		account := types.NewAddressByStr(addr)
		return config.GetAccountNonce(account)
	}

	mempoolConf := mempool.Config{
		ID:              config.ID,
		Logger:          &order.Logger{FieldLogger: config.Logger},
		BatchSize:       config.Config.Solo.Mempool.BatchSize,
		PoolSize:        config.Config.Solo.Mempool.PoolSize,
		GetAccountNonce: fn,
		IsTimed:         config.Config.TimedGenBlock.Enable,
	}
	mempoolInst := mempool.NewMempool[types.Transaction, *types.Transaction](mempoolConf)
	// init batch timer manager
	recvCh := make(chan consensusEvent, maxChanSize)
	batchTimerMgr := NewTimerManager(recvCh, config.Logger)
	batchTimerMgr.newTimer(Batch, config.Config.Solo.Mempool.BatchTimeout.ToDuration())
	batchTimerMgr.newTimer(NoTxBatch, config.Config.TimedGenBlock.NoTxBatchTimeout.ToDuration())

	soloNode := &Node{
		ID:               config.ID,
		isTimed:          mempoolConf.IsTimed,
		noTxBatchTimeout: config.Config.TimedGenBlock.NoTxBatchTimeout.ToDuration(),
		batchTimeout:     config.Config.Solo.Mempool.BatchTimeout.ToDuration(),
		blockCh:          make(chan *mempool.RequestHashBatch[types.Transaction, *types.Transaction], maxChanSize),
		commitC:          make(chan *types.CommitEvent, maxChanSize),
		recvCh:           recvCh,
		lastExec:         config.Applied,
		mempool:          mempoolInst,
		batchMgr:         batchTimerMgr,
		peerMgr:          config.PeerMgr,
		logger:           config.Logger,
	}
	soloNode.logger.Infof("SOLO lastExec = %d", soloNode.lastExec)
	soloNode.logger.Infof("SOLO no-tx batch timeout = %v", config.Config.TimedGenBlock.NoTxBatchTimeout.ToDuration())
	soloNode.logger.Infof("SOLO batch timeout = %v", config.Config.Solo.Mempool.BatchTimeout.ToDuration())
	soloNode.logger.Infof("SOLO batch size = %d", config.Config.Solo.Mempool.BatchSize)
	soloNode.logger.Infof("SOLO pool size = %d", config.Config.Solo.Mempool.PoolSize)
	return soloNode, nil
}

func (n *Node) SubmitTxsFromRemote(_ [][]byte) error {
	return nil
}

func (n *Node) listenEvent() {
	for {
		select {
		case <-n.ctx.Done():
			n.logger.Info("----- Exit listen event -----")
			return

		case ev := <-n.recvCh:
			switch e := ev.(type) {
			// handle report state
			case *chainState:
				if e.Height%10 == 0 {
					n.logger.WithFields(logrus.Fields{
						"height": e.Height,
						"hash":   e.BlockHash.String(),
					}).Info("Report checkpoint")
				}
				if len(e.TxHashList) != 0 {
					digestList := []string{e.BlockHash.String()}
					n.mempool.RemoveBatches(digestList)
				}

			// receive tx from api
			case *txcache.TxWithResp:
				// stop no-tx batch timer when this node receives the first transaction
				if n.batchMgr.isTimerActive(NoTxBatch) {
					n.batchMgr.stopTimer(NoTxBatch)
				}
				// start batch timer when this node receives the first transaction
				if !n.batchMgr.isTimerActive(Batch) {
					n.batchMgr.startTimer(Batch)
				}
				var requests [][]byte
				tx := e.Tx
				raw, err := tx.RbftMarshal()
				if err != nil {
					n.logger.Error(err)
				} else {
					requests = append(requests, raw)
				}

				if len(requests) != 0 {
					if batches, _ := n.mempool.AddNewRequests(requests, true, true, false); batches != nil {
						n.batchMgr.stopTimer(Batch)
						if len(batches) != 1 {
							n.logger.Errorf("batch size is not 1, actual: %d", len(batches))
							continue
						}
						n.postProposal(batches[0])
						// start no-tx batch timer when this node handle the last transaction
						if n.isTimed && !n.mempool.HasPendingRequestInPool() {
							if !n.batchMgr.isTimerActive(NoTxBatch) {
								n.batchMgr.startTimer(NoTxBatch)
							}
						}
					}
				}
				e.Ch <- true

			// handle timeout event
			case batchTimeoutEvent:
				if err := n.processBatchTimeout(e); err != nil {
					n.logger.Errorf("Process batch timeout failed: %v", err)
					continue
				}
			}
		}
	}
}

func (n *Node) processBatchTimeout(e batchTimeoutEvent) error {
	switch e {
	case Batch:
		n.batchMgr.stopTimer(Batch)
		n.logger.Debug("Batch timer expired, try to create a batch")
		if n.mempool.HasPendingRequestInPool() {
			if batches := n.mempool.GenerateRequestBatch(); batches != nil {
				for _, batch := range batches {
					n.postProposal(batch)
				}
				n.batchMgr.startTimer(Batch)

				// check if there is no tx in the mempool, start the no tx batch timer
				if n.isTimed && !n.mempool.HasPendingRequestInPool() {
					if !n.batchMgr.isTimerActive(NoTxBatch) {
						n.batchMgr.startTimer(NoTxBatch)
					}
				}
			}
		} else {
			n.logger.Debug("The length of priorityIndex is 0, skip the batch timer")
		}
	case NoTxBatch:
		if !n.isTimed {
			return fmt.Errorf("the node is not support the no-tx batch, skip the timer")
		}
		if !n.mempool.HasPendingRequestInPool() {
			n.batchMgr.stopTimer(NoTxBatch)
			n.logger.Debug("start create empty block")
			batches := n.mempool.GenerateRequestBatch()
			if batches == nil {
				return fmt.Errorf("create empty block failed, the length of batches is 0")
			}
			if len(batches) != 1 {
				return fmt.Errorf("create empty block failed, the expect length of batches is 1, but actual is %d", len(batches))
			}
			n.postProposal(batches[0])
			if !n.batchMgr.isTimerActive(NoTxBatch) {
				n.batchMgr.startTimer(NoTxBatch)
			}
		}
	}
	return nil
}

// Schedule to collect txs to the listenReadyBlock channel
func (n *Node) listenReadyBlock() {

	for {
		select {
		case <-n.ctx.Done():
			n.logger.Info("----- Exit listen ready block loop -----")
			return
		case e := <-n.blockCh:
			n.logger.WithFields(logrus.Fields{
				"batch_hash": e.BatchHash,
				"tx_count":   len(e.TxList),
			}).Debugf("Receive proposal from txcache")
			n.logger.Infof("======== Call execute, height=%d", n.lastExec+1)

			txs := make([]*types.Transaction, 0, len(e.TxList))
			for _, data := range e.TxList {
				tx := &types.Transaction{}
				if err := tx.Unmarshal(data); err != nil {
					n.logger.Errorf("Unmarshal tx failed: %v", err)
					continue
				}
				txs = append(txs, tx)
			}

			block := &types.Block{
				BlockHeader: &types.BlockHeader{
					Version:   []byte("1.0.0"),
					Number:    n.lastExec + 1,
					Timestamp: e.Timestamp,
				},
				Transactions: txs,
			}
			localList := make([]bool, len(e.TxList))
			for i := 0; i < len(e.TxList); i++ {
				localList[i] = true
			}
			executeEvent := &types.CommitEvent{
				Block:     block,
				LocalList: localList,
			}
			n.commitC <- executeEvent
			n.lastExec++
		}
	}
}

func (n *Node) postProposal(batch *mempool.RequestHashBatch[types.Transaction, *types.Transaction]) {
	n.blockCh <- batch
}
