package coreapi

import (
	"errors"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/axiomesh/axiom-kit/types"
	"github.com/axiomesh/axiom-ledger/internal/coreapi/api"
)

type ChainAPI CoreAPI

var _ api.ChainAPI = (*ChainAPI)(nil)

func (api *ChainAPI) Status() string {
	if api.axiomLedger.Repo.ReadonlyMode {
		return "normal"
	}

	err := api.axiomLedger.Consensus.Ready()
	if err != nil {
		return "abnormal"
	}

	return "normal"
}

func (api *ChainAPI) Meta() (*types.ChainMeta, error) {
	return api.axiomLedger.ViewLedger.ChainLedger.GetChainMeta(), nil
}

func (api *ChainAPI) TPS(begin, end uint64) (uint64, error) {
	var (
		errCount  atomic.Int64
		total     atomic.Uint64
		startTime int64
		endTime   int64
		wg        sync.WaitGroup
	)

	if int(begin) <= 0 {
		return 0, errors.New("begin number should be greater than zero")
	}

	if int(begin) >= int(end) {
		return 0, errors.New("begin number should be smaller than end number")
	}

	wg.Add(int(end - begin + 1))
	for i := begin + 1; i <= end-1; i++ {
		go func(height uint64, wg *sync.WaitGroup) {
			defer wg.Done()
			count, err := api.axiomLedger.ViewLedger.ChainLedger.GetTransactionCount(height)
			if err != nil {
				errCount.Inc()
			} else {
				total.Add(count)
			}
		}(i, &wg)
	}

	go func() {
		defer wg.Done()
		block, err := api.axiomLedger.ViewLedger.ChainLedger.GetBlock(begin)
		if err != nil {
			errCount.Inc()
		} else {
			total.Add(uint64(len(block.Transactions)))
			startTime = block.BlockHeader.Timestamp
		}
	}()
	go func() {
		defer wg.Done()
		block, err := api.axiomLedger.ViewLedger.ChainLedger.GetBlock(end)
		if err != nil {
			errCount.Inc()
		} else {
			total.Add(uint64(len(block.Transactions)))
			endTime = block.BlockHeader.Timestamp
		}
	}()
	wg.Wait()

	if errCount.Load() != 0 {
		return 0, errors.New("error during get block TPS")
	}

	elapsed := (endTime - startTime) / int64(time.Second)

	if elapsed <= 0 {
		return 0, errors.New("incorrect block timestamp")
	}
	return total.Load() / uint64(elapsed), nil
}
