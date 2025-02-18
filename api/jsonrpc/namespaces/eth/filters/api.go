// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package filters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethereumTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/sirupsen/logrus"

	"github.com/axiomesh/axiom-kit/types"
	"github.com/axiomesh/axiom-ledger/api/jsonrpc/namespaces/eth"
	rpctypes "github.com/axiomesh/axiom-ledger/api/jsonrpc/types"
	"github.com/axiomesh/axiom-ledger/internal/coreapi/api"
	"github.com/axiomesh/axiom-ledger/pkg/repo"
)

// filter is a helper struct that holds meta information over the filter type
// and associated subscription in the event system.
type filter struct {
	typ      Type
	deadline *time.Timer // filter is inactiv when deadline triggers
	hashes   []*types.Hash
	fullTx   bool
	txs      []*types.Transaction
	crit     FilterQuery
	logs     []*types.EvmLog
	s        *Subscription // associated subscription in event system
}

// FilterAPI offers support to create and manage filters. This will allow external clients to retrieve various
// information related to the Ethereum protocol such als blocks, transactions and logs.
type FilterAPI struct {
	rep       *repo.Repo
	api       api.CoreAPI
	mux       *event.TypeMux
	quit      chan struct{}
	events    *EventSystem
	filtersMu sync.Mutex
	filters   map[rpc.ID]*filter
	timeout   time.Duration
	logger    logrus.FieldLogger
}

// NewPublicFilterAPI returns a new PublicFilterAPI instance.
func NewAPI(rep *repo.Repo, api api.CoreAPI, logger logrus.FieldLogger) *FilterAPI {
	filterAPI := &FilterAPI{
		rep:     rep,
		api:     api,
		events:  NewEventSystem(api, false),
		filters: make(map[rpc.ID]*filter),
		timeout: rpctypes.Deadline,
		logger:  logger,
	}
	go filterAPI.timeoutLoop()

	return filterAPI
}

// timeoutLoop runs every 5 minutes and deletes filters that have not been recently used.
// Tt is started when the api is created.
func (api *FilterAPI) timeoutLoop() {
	var toUninstall []*Subscription
	ticker := time.NewTicker(api.timeout)
	defer ticker.Stop()
	for {
		<-ticker.C
		api.filtersMu.Lock()
		for id, f := range api.filters {
			select {
			case <-f.deadline.C:
				toUninstall = append(toUninstall, f.s)
				delete(api.filters, id)
			default:
				continue
			}
		}
		api.filtersMu.Unlock()

		// Unsubscribes are processed outside the lock to avoid the following scenario:
		// event loop attempts broadcasting events to still active filters while
		// Unsubscribe is waiting for it to process the uninstall request.
		for _, s := range toUninstall {
			s.Unsubscribe()
		}
		toUninstall = nil
	}
}

// NewPendingTransactionFilter creates a filter that fetches pending transaction txs
// as transactions enter the pending state.
//
// It is part of the filter package because this filter can be used through the
// `eth_getFilterChanges` polling method that is also used for log filters.
//
// https://eth.wiki/json-rpc/API#eth_newpendingtransactionfilter
func (api *FilterAPI) NewPendingTransactionFilter(fullTx *bool) rpc.ID {
	var (
		pendingTxs   = make(chan []*types.Transaction)
		pendingTxSub = api.events.SubscribePendingTxs(pendingTxs)
	)

	api.filtersMu.Lock()
	api.filters[pendingTxSub.ID] = &filter{typ: PendingTransactionsSubscription, fullTx: fullTx != nil && *fullTx, deadline: time.NewTimer(api.timeout), txs: make([]*types.Transaction, 0), s: pendingTxSub}
	api.filtersMu.Unlock()

	go func() {
		for {
			select {
			case ph := <-pendingTxs:
				api.filtersMu.Lock()
				if f, found := api.filters[pendingTxSub.ID]; found {
					f.txs = append(f.txs, ph...)
				}
				api.filtersMu.Unlock()
			case <-pendingTxSub.Err():
				api.filtersMu.Lock()
				delete(api.filters, pendingTxSub.ID)
				api.filtersMu.Unlock()
				return
			}
		}
	}()

	return pendingTxSub.ID
}

// NewPendingTransactions creates a subscription that is triggered each time a transaction
// enters the transaction pool and was signed from one of the transactions this nodes manages.
func (api *FilterAPI) NewPendingTransactions(ctx context.Context, fullTx *bool) (*rpc.Subscription, error) {
	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		txs := make(chan []*types.Transaction, 128)
		pendingTxSub := api.events.SubscribePendingTxs(txs)

		for {
			select {
			case txs := <-txs:
				// To keep the original behaviour, send a single tx hash in one notification.
				// TODO(rjl493456442) Send a batch of tx txs in one notification
				for _, tx := range txs {
					if fullTx != nil && *fullTx {
						rpcTx := eth.NewRPCTransaction(tx, common.Hash{}, 0, 0)
						err := notifier.Notify(rpcSub.ID, rpcTx)
						if err != nil {
							api.logger.Warn("notifier notify error", err)
						}
					} else {
						err := notifier.Notify(rpcSub.ID, tx.GetHash().ETHHash())
						if err != nil {
							api.logger.Warn("notifier notify error", err)
						}
					}
				}
			case <-rpcSub.Err():
				pendingTxSub.Unsubscribe()
				return
			case <-notifier.Closed():
				pendingTxSub.Unsubscribe()
				return
			}
		}
	}()

	return rpcSub, nil
}

// NewBlockFilter creates a filter that fetches blocks that are imported into the chain.
// It is part of the filter package since polling goes with eth_getFilterChanges.
//
// https://eth.wiki/json-rpc/API#eth_newblockfilter
func (api *FilterAPI) NewBlockFilter() rpc.ID {
	var (
		headers   = make(chan *types.BlockHeader)
		headerSub = api.events.SubscribeNewHeads(headers)
	)

	api.filtersMu.Lock()
	api.filters[headerSub.ID] = &filter{typ: BlocksSubscription, deadline: time.NewTimer(api.timeout), hashes: make([]*types.Hash, 0), s: headerSub}
	api.filtersMu.Unlock()

	go func() {
		for {
			select {
			case h := <-headers:
				api.filtersMu.Lock()
				if f, found := api.filters[headerSub.ID]; found {
					f.hashes = append(f.hashes, h.Hash())
				}
				api.filtersMu.Unlock()
			case <-headerSub.Err():
				api.filtersMu.Lock()
				delete(api.filters, headerSub.ID)
				api.filtersMu.Unlock()
				return
			}
		}
	}()

	return headerSub.ID
}

// NewHeads send a notification each time a new (header) block is appended to the chain.
func (api *FilterAPI) NewHeads(ctx context.Context) (*rpc.Subscription, error) {
	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	rpcSub := notifier.CreateSubscription()

	go func() {
		headers := make(chan *types.BlockHeader)
		headersSub := api.events.SubscribeNewHeads(headers)

		for {
			select {
			case h := <-headers:
				ethHeader := formatEthHeader(h)
				err := notifier.Notify(rpcSub.ID, ethHeader)
				if err != nil {
					api.logger.Warn("notifier notify error", err)
				}
			case <-rpcSub.Err():
				headersSub.Unsubscribe()
				return
			case <-notifier.Closed():
				headersSub.Unsubscribe()
				return
			}
		}
	}()

	return rpcSub, nil
}

func formatEthHeader(h *types.BlockHeader) *ethereumTypes.Header {
	bloom := ethereumTypes.Bloom{}
	bloom.Add(h.Bloom.Bytes())
	return &ethereumTypes.Header{
		ParentHash:  h.ParentHash.ETHHash(),
		Root:        h.StateRoot.ETHHash(),
		TxHash:      h.TxRoot.ETHHash(),
		ReceiptHash: h.ReceiptRoot.ETHHash(),
		Bloom:       bloom,
		Number:      big.NewInt(0).SetUint64(h.Number),
		Time:        uint64(h.Timestamp),
	}
}

// Logs creates a subscription that fires for all new log that match the given filter criteria.
func (api *FilterAPI) Logs(ctx context.Context, crit FilterCriteria) (*rpc.Subscription, error) {
	api.logger.Debugf("eth_logs: crit: %s", crit)
	notifier, supported := rpc.NotifierFromContext(ctx)
	if !supported {
		return &rpc.Subscription{}, rpc.ErrNotificationsUnsupported
	}

	var (
		rpcSub      = notifier.CreateSubscription()
		matchedLogs = make(chan []*types.EvmLog)
	)

	logsSub, err := api.events.SubscribeLogs(crit.toAxmFilterQuery(), matchedLogs)
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			select {
			case logs := <-matchedLogs:
				for _, log := range logs {
					ethLog := formatEthLogs(log)
					err := notifier.Notify(rpcSub.ID, &ethLog)
					if err != nil {
						api.logger.Warn("notifier notify error", err)
					}
				}
			case <-rpcSub.Err(): // client send an unsubscribe request
				logsSub.Unsubscribe()
				return
			case <-notifier.Closed(): // connection dropped
				logsSub.Unsubscribe()
				return
			}
		}
	}()

	return rpcSub, nil
}

func formatEthLogs(evmlog *types.EvmLog) *ethereumTypes.Log {
	topics := make([]common.Hash, 0)
	for _, topic := range evmlog.Topics {
		topics = append(topics, topic.ETHHash())
	}
	return &ethereumTypes.Log{
		Address:     evmlog.Address.ETHAddress(),
		Topics:      topics,
		Data:        evmlog.Data,
		BlockNumber: evmlog.BlockNumber,
		TxHash:      evmlog.TransactionHash.ETHHash(),
		TxIndex:     uint(evmlog.TransactionIndex),
		BlockHash:   evmlog.BlockHash.ETHHash(),
		Index:       uint(evmlog.LogIndex),
		Removed:     evmlog.Removed,
	}
}

// FilterCriteria represents a request to create a new filter.
// Same as ethereum.FilterQuery but with UnmarshalJSON() method.
type FilterCriteria ethereum.FilterQuery

// NewFilter creates a new filter and returns the filter id. It can be
// used to retrieve logs when the state changes. This method cannot be
// used to fetch logs that are already stored in the state.
//
// Default criteria for the from and to block are "latest".
// Using "latest" as block number will return logs for mined blocks.
// Using "pending" as block number returns logs for not yet mined (pending) blocks.
// In case logs are removed (chain reorg) previously returned logs are returned
// again but with the removed property set to true.
//
// In case "fromBlock" > "toBlock" an error is returned.
//
// https://eth.wiki/json-rpc/API#eth_newfilter
func (api *FilterAPI) NewFilter(crit FilterCriteria) (rpc.ID, error) {
	logs := make(chan []*types.EvmLog)
	logsSub, err := api.events.SubscribeLogs(crit.toAxmFilterQuery(), logs)
	if err != nil {
		return "", err
	}

	api.filtersMu.Lock()
	api.filters[logsSub.ID] = &filter{typ: LogsSubscription, crit: crit.toAxmFilterQuery(), deadline: time.NewTimer(api.timeout), logs: make([]*types.EvmLog, 0), s: logsSub}
	api.filtersMu.Unlock()

	go func() {
		for {
			select {
			case l := <-logs:
				api.filtersMu.Lock()
				if f, found := api.filters[logsSub.ID]; found {
					f.logs = append(f.logs, l...)
				}
				api.filtersMu.Unlock()
			case <-logsSub.Err():
				api.filtersMu.Lock()
				delete(api.filters, logsSub.ID)
				api.filtersMu.Unlock()
				return
			}
		}
	}()

	return logsSub.ID, nil
}

// GetLogs returns logs matching the given argument that are stored within the state.
//
// https://eth.wiki/json-rpc/API#eth_getlogs
func (api *FilterAPI) GetLogs(ctx context.Context, ethCrit FilterCriteria) ([]*ethereumTypes.Log, error) {
	api.logger.Debugf("eth_getLogs: ethCrit: %s", ethCrit)
	var filter *Filter
	crit := ethCrit.toAxmFilterQuery()
	if crit.BlockHash != nil {
		// Block filter requested, construct a single-shot filter
		filter = NewBlockFilter(api.api, crit.BlockHash, crit.Addresses, crit.Topics)
	} else {
		// Convert the RPC block numbers into internal representations
		begin := rpc.LatestBlockNumber.Int64()
		if crit.FromBlock != nil {
			begin = crit.FromBlock.Int64()
			if crit.FromBlock.Int64() == rpc.EarliestBlockNumber.Int64() {
				begin = int64(api.rep.Config.Genesis.EpochInfo.StartBlock)
			}
		}
		end := rpc.LatestBlockNumber.Int64()
		if crit.ToBlock != nil {
			end = crit.ToBlock.Int64()
		}
		// Construct the range filter
		filter = NewRangeFilter(api.api, begin, end, crit.Addresses, crit.Topics)
	}
	// Run the filter and return all the logs
	logs, err := filter.Logs(ctx)
	if err != nil {
		return nil, err
	}

	return returnLogs(logs), err
}

// UninstallFilter removes the filter with the given filter id.
//
// https://eth.wiki/json-rpc/API#eth_uninstallfilter
func (api *FilterAPI) UninstallFilter(id rpc.ID) bool {
	api.filtersMu.Lock()
	f, found := api.filters[id]
	if found {
		delete(api.filters, id)
	}
	api.filtersMu.Unlock()
	if found {
		f.s.Unsubscribe()
	}

	return found
}

// GetFilterLogs returns the logs for the filter with the given id.
// If the filter could not be found an empty array of logs is returned.
//
// https://eth.wiki/json-rpc/API#eth_getfilterlogs
func (api *FilterAPI) GetFilterLogs(ctx context.Context, id rpc.ID) ([]*ethereumTypes.Log, error) {
	api.filtersMu.Lock()
	f, found := api.filters[id]
	api.filtersMu.Unlock()

	if !found || f.typ != LogsSubscription {
		return nil, errors.New("filter not found")
	}

	var filter *Filter
	if f.crit.BlockHash != nil {
		// Block filter requested, construct a single-shot filter
		filter = NewBlockFilter(api.api, f.crit.BlockHash, f.crit.Addresses, f.crit.Topics)
	} else {
		// Convert the RPC block numbers into internal representations
		begin := rpc.LatestBlockNumber.Int64()
		if f.crit.FromBlock != nil {
			begin = f.crit.FromBlock.Int64()
			if f.crit.FromBlock.Int64() == rpc.EarliestBlockNumber.Int64() {
				begin = int64(api.rep.Config.Genesis.EpochInfo.StartBlock)
			}
		}
		end := rpc.LatestBlockNumber.Int64()
		if f.crit.ToBlock != nil {
			end = f.crit.ToBlock.Int64()
		}
		// Construct the range filter
		filter = NewRangeFilter(api.api, begin, end, f.crit.Addresses, f.crit.Topics)
	}
	// Run the filter and return all the logs
	logs, err := filter.Logs(ctx)
	if err != nil {
		return nil, err
	}
	return returnLogs(logs), nil
}

// GetFilterChanges returns the logs for the filter with the given id since
// last time it was called. This can be used for polling.
//
// For pending transaction and block filters the result is []common.Hash.
// (pending)Log filters return []Log.
//
// https://eth.wiki/json-rpc/API#eth_getfilterchanges
func (api *FilterAPI) GetFilterChanges(id rpc.ID) (any, error) {
	api.filtersMu.Lock()
	defer api.filtersMu.Unlock()

	if f, found := api.filters[id]; found {
		if !f.deadline.Stop() {
			// timer expired but filter is not yet removed in timeout loop
			// receive timer value and reset timer
			<-f.deadline.C
		}
		f.deadline.Reset(api.timeout)

		switch f.typ {
		case PendingTransactionsSubscription, BlocksSubscription:
			hashes := f.hashes
			f.hashes = nil
			return returnHashes(hashes), nil
		case LogsSubscription, MinedAndPendingLogsSubscription:
			logs := f.logs
			f.logs = nil
			return returnLogs(logs), nil
		}
	}

	return []any{}, errors.New("filter not found")
}

// returnHashes is a helper that will return an empty hash array case the given hash array is nil,
// otherwise the given hashes array is returned.
func returnHashes(hashes []*types.Hash) []*types.Hash {
	if hashes == nil {
		return []*types.Hash{}
	}
	return hashes
}

// returnLogs is a helper that will return an empty log array in case the given logs array is nil,
// otherwise the given logs array is returned.
func returnLogs(logs []*types.EvmLog) []*ethereumTypes.Log {
	if logs == nil {
		return []*ethereumTypes.Log{}
	}

	res := make([]*ethereumTypes.Log, len(logs))
	for i := 0; i < len(logs); i++ {
		res[i] = formatEthLogs(logs[i])
	}
	return res
}

// UnmarshalJSON sets *args fields with given data.
func (args *FilterCriteria) UnmarshalJSON(data []byte) error {
	type input struct {
		BlockHash *common.Hash     `json:"blockHash"`
		FromBlock *rpc.BlockNumber `json:"fromBlock"`
		ToBlock   *rpc.BlockNumber `json:"toBlock"`
		Addresses any              `json:"address"`
		Topics    []any            `json:"topics"`
	}

	var raw input
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.BlockHash != nil {
		if raw.FromBlock != nil || raw.ToBlock != nil {
			// BlockHash is mutually exclusive with FromBlock/ToBlock criteria
			return errors.New("cannot specify both BlockHash and FromBlock/ToBlock, choose one or the other")
		}
		args.BlockHash = raw.BlockHash
	} else {
		if raw.FromBlock != nil {
			args.FromBlock = big.NewInt(raw.FromBlock.Int64())
		}

		if raw.ToBlock != nil {
			args.ToBlock = big.NewInt(raw.ToBlock.Int64())
		}
	}

	args.Addresses = []common.Address{}

	if raw.Addresses != nil {
		// raw.Address can contain a single address or an array of addresses
		switch rawAddr := raw.Addresses.(type) {
		case []any:
			for i, addr := range rawAddr {
				if strAddr, ok := addr.(string); ok {
					addr, err := decodeAddress(strAddr)
					if err != nil {
						return fmt.Errorf("invalid address at index %d: %v", i, err)
					}
					args.Addresses = append(args.Addresses, addr)
				} else {
					return fmt.Errorf("non-string address at index %d", i)
				}
			}
		case string:
			addr, err := decodeAddress(rawAddr)
			if err != nil {
				return fmt.Errorf("invalid address: %v", err)
			}
			args.Addresses = []common.Address{addr}
		default:
			return errors.New("invalid addresses in query")
		}
	}

	// topics is an array consisting of strings and/or arrays of strings.
	// JSON null values are converted to common.Hash{} and ignored by the filter manager.
	if len(raw.Topics) > 0 {
		args.Topics = make([][]common.Hash, len(raw.Topics))
		for i, t := range raw.Topics {
			switch topic := t.(type) {
			case nil:
				// ignore topic when matching logs

			case string:
				// match specific topic
				top, err := decodeTopic(topic)
				if err != nil {
					return err
				}
				args.Topics[i] = []common.Hash{top}

			case []any:
				// or case e.g. [null, "topic0", "topic1"]
				for _, rawTopic := range topic {
					if rawTopic == nil {
						// null component, match all
						args.Topics[i] = nil
						break
					}
					if topic, ok := rawTopic.(string); ok {
						parsed, err := decodeTopic(topic)
						if err != nil {
							return err
						}
						args.Topics[i] = append(args.Topics[i], parsed)
					} else {
						return errors.New("invalid topic(s)")
					}
				}
			default:
				return errors.New("invalid topic(s)")
			}
		}
	}

	return nil
}

func decodeAddress(s string) (common.Address, error) {
	b, err := hexutil.Decode(s)
	if err == nil && len(b) != common.AddressLength {
		err = fmt.Errorf("hex has invalid length %d after decoding; expected %d for address", len(b), common.AddressLength)
	}
	return common.BytesToAddress(b), err
}

func decodeTopic(s string) (common.Hash, error) {
	b, err := hexutil.Decode(s)
	if err == nil && len(b) != common.HashLength {
		err = fmt.Errorf("hex has invalid length %d after decoding; expected %d for topic", len(b), common.HashLength)
	}
	return common.BytesToHash(b), err
}

func (fc *FilterCriteria) toAxmFilterQuery() FilterQuery {
	fq := FilterQuery{
		FromBlock: fc.FromBlock,
		ToBlock:   fc.ToBlock,
	}

	if fc.BlockHash != nil {
		fq.BlockHash = types.NewHash(fc.BlockHash.Bytes())
	}

	for _, addr := range fc.Addresses {
		fq.Addresses = append(fq.Addresses, types.NewAddress(addr.Bytes()))
	}

	for _, topicList := range fc.Topics {
		var topics []*types.Hash
		for _, topic := range topicList {
			topics = append(topics, types.NewHash(topic.Bytes()))
		}
		fq.Topics = append(fq.Topics, topics)
	}

	return fq
}
