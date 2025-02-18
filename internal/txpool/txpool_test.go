package txpool

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rbft "github.com/axiomesh/axiom-bft"

	"github.com/axiomesh/axiom-ledger/internal/components/timer"

	log2 "github.com/axiomesh/axiom-kit/log"
	"github.com/axiomesh/axiom-kit/txpool"

	"github.com/axiomesh/axiom-kit/types"
)

func TestNewTxPool(t *testing.T) {
	ast := assert.New(t)
	conf := NewMockTxPoolConfig()
	pool, err := NewTxPool[types.Transaction, *types.Transaction](conf)
	ast.Nil(err)
	ast.False(pool.IsPoolFull())
}

func TestTxPoolImpl_Start(t *testing.T) {
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	err := pool.Start()
	ast.Nil(err)

	wrongEvent := &types.Block{}
	pool.recvCh <- wrongEvent
	pool.Stop()

	ctx, cancel := context.WithCancel(context.Background())

	log := log2.NewWithModule("txpool")
	pool = &txPoolImpl[types.Transaction, *types.Transaction]{
		logger:   log,
		recvCh:   make(chan txPoolEvent, maxChanSize),
		timerMgr: timer.NewTimerManager(log),

		ctx:    ctx,
		cancel: cancel,
	}
	err = pool.Start()
	ast.NotNil(err)
	ast.Contains(err.Error(), "timer RemoveTx doesn't exist")
	pool.Stop()
}

func TestTxPoolImpl_AddLocalTx(t *testing.T) {
	t.Parallel()
	t.Run("nonce is wanted", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		from := s.Addr.String()
		tx := constructTx(s, 0)
		err = pool.AddLocalTx(tx)
		ast.Nil(err)
		ast.NotNil(pool.txStore.allTxs[from])
		ast.Equal(1, len(pool.txStore.allTxs[from].items))
		ast.Equal(1, len(pool.txStore.txHashMap))
		ast.Equal(uint64(0), pool.txStore.txHashMap[tx.RbftGetTxHash()].nonce)
		ast.Equal(uint64(1), pool.txStore.priorityNonBatchSize)
		ast.Equal(1, pool.txStore.priorityIndex.size())
		ast.Equal(0, pool.txStore.parkingLotIndex.size())
		ast.Equal(1, pool.txStore.localTTLIndex.size())
		ast.Equal(1, pool.txStore.removeTTLIndex.size())
		poolTx := pool.txStore.allTxs[from].items[0]
		ast.Equal(tx.RbftGetTxHash(), poolTx.rawTx.RbftGetTxHash())
	})

	t.Run("pool is full", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		pool.poolMaxSize = 1
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)
		ast.False(pool.statusMgr.In(PoolFull))

		s, err := types.GenerateSigner()
		ast.Nil(err)
		tx := constructTx(s, 0)
		err = pool.AddLocalTx(tx)
		ast.Nil(err)
		// because add remote txs is async,
		// so we need to send getPendingTxByHash event to ensure last event is handled
		ast.Equal(tx.RbftGetTxHash(), pool.GetPendingTxByHash(tx.RbftGetTxHash()).RbftGetTxHash())
		ast.True(pool.statusMgr.In(PoolFull))

		tx1 := constructTx(s, 1)
		err = pool.AddLocalTx(tx1)
		ast.NotNil(err)
		ast.Contains(err.Error(), ErrTxPoolFull.Error())
	})

	t.Run("nonce is bigger", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		from := s.Addr.String()
		tx := constructTx(s, 1)
		err = pool.AddLocalTx(tx)
		ast.Nil(err)
		ast.NotNil(pool.txStore.allTxs[from])
		ast.Equal(1, len(pool.txStore.allTxs[from].items))
		ast.Equal(1, len(pool.txStore.txHashMap))
		ast.Equal(uint64(1), pool.txStore.txHashMap[tx.RbftGetTxHash()].nonce)
		ast.Equal(uint64(0), pool.txStore.priorityNonBatchSize)
		ast.Equal(0, pool.txStore.priorityIndex.size())
		ast.Equal(1, pool.txStore.parkingLotIndex.size())
		poolTx := pool.txStore.allTxs[from].items[1]
		ast.Equal(tx.RbftGetTxHash(), poolTx.rawTx.RbftGetTxHash())

		tx = constructTx(s, 0)
		err = pool.AddLocalTx(tx)
		ast.Nil(err)
		ast.Equal(uint64(2), pool.txStore.priorityNonBatchSize, "we receive wanted nonce")
		ast.Equal(2, pool.txStore.localTTLIndex.size())
		ast.Equal(2, pool.txStore.removeTTLIndex.size())
		ast.Equal(2, pool.txStore.priorityIndex.size())
		ast.Equal(1, pool.txStore.parkingLotIndex.size(), "decrease it when we trigger removeBatch")
	})

	t.Run("nonce is lower", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		from := s.Addr.String()
		txs := constructTxs(s, 10)
		for _, tx := range txs {
			err = pool.AddLocalTx(tx)
			ast.Nil(err)
		}

		ast.NotNil(pool.txStore.allTxs[from])
		ast.Equal(10, len(pool.txStore.allTxs[from].items))
		ast.Equal(10, len(pool.txStore.txHashMap))
		ast.Equal(uint64(10), pool.txStore.priorityNonBatchSize)
		ast.Equal(10, pool.txStore.priorityIndex.size())
		ast.Equal(0, pool.txStore.parkingLotIndex.size())

		lowTx := txs[0]
		err = pool.AddLocalTx(lowTx)
		ast.NotNil(err)
		ast.Contains(err.Error(), ErrNonceTooLow.Error())
	})

	t.Run("duplicate tx", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		tx := constructTx(s, 0)
		err = pool.AddLocalTx(tx)
		ast.Nil(err)

		err = pool.AddLocalTx(tx)
		ast.NotNil(err)
		ast.Contains(err.Error(), ErrNonceTooLow.Error())

		highTx := constructTx(s, 10)
		err = pool.AddLocalTx(highTx)
		ast.Nil(err)

		err = pool.AddLocalTx(highTx)
		ast.NotNil(err)
		ast.Contains(err.Error(), ErrDuplicateTx.Error())
	})

	t.Run("duplicate nonce but not the same tx", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		from := s.Addr.String()
		ast.Nil(err)
		tx01 := constructTx(s, 0)
		err = pool.AddLocalTx(tx01)
		ast.Nil(err)
		ast.Equal(1, pool.txStore.localTTLIndex.size())
		ast.Equal(1, pool.txStore.removeTTLIndex.size())

		tx02, err := types.GenerateTransactionWithSigner(0, to, big.NewInt(1000), nil, s)
		ast.Nil(err)

		err = pool.AddLocalTx(tx02)
		ast.NotNil(err)
		ast.Contains(err.Error(), ErrNonceTooLow.Error())

		tx21 := constructTx(s, 2)
		err = pool.AddLocalTx(tx21)
		ast.Nil(err)
		poolTx := pool.txStore.getPoolTxByTxnPointer(from, tx21.RbftGetNonce())
		ast.Equal(tx21.RbftGetTxHash(), poolTx.rawTx.RbftGetTxHash(), "tx21 exist in txpool")
		ast.Equal(2, pool.txStore.localTTLIndex.size())
		ast.Equal(2, pool.txStore.removeTTLIndex.size())

		tx22, err := types.GenerateTransactionWithSigner(2, to, big.NewInt(1000), nil, s)
		ast.Nil(err)
		ast.Equal(tx21.RbftGetNonce(), tx22.RbftGetNonce())

		err = pool.AddLocalTx(tx22)
		ast.Nil(err)
		ast.NotEqual(tx21.RbftGetTxHash(), tx22.RbftGetTxHash())
		poolTx = pool.txStore.getPoolTxByTxnPointer(from, tx22.RbftGetNonce())
		ast.Equal(tx22.RbftGetTxHash(), poolTx.rawTx.RbftGetTxHash(), "tx22 replaced tx21")
		ast.Equal(2, pool.txStore.localTTLIndex.size())
		ast.Equal(2, pool.txStore.removeTTLIndex.size())
		removeKey := &orderedIndexKey{
			account: poolTx.getAccount(),
			nonce:   poolTx.rawTx.RbftGetNonce(),
			time:    poolTx.arrivedTime,
		}
		v := pool.txStore.removeTTLIndex.data.Get(removeKey)
		ast.NotNil(v)
		localKey := &orderedIndexKey{
			account: poolTx.getAccount(),
			nonce:   poolTx.rawTx.RbftGetNonce(),
			time:    poolTx.getRawTimestamp(),
		}
		v = pool.txStore.localTTLIndex.data.Get(localKey)
		ast.NotNil(v)
	})

	t.Run("nonce is bigger than tolerance, trigger remove high nonce tx", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.toleranceNonceGap = 1
		err := pool.Start()
		defer pool.Stop()

		s, err := types.GenerateSigner()
		tx0 := constructTx(s, 0)
		err = pool.AddLocalTx(tx0)
		ast.Nil(err)

		tx2 := constructTx(s, 2)
		err = pool.AddLocalTx(tx2)
		ast.Nil(err)
		ast.Equal(2, len(pool.txStore.allTxs[tx2.RbftGetFrom()].items))
		ast.Equal(2, pool.txStore.localTTLIndex.size())
		ast.Equal(2, pool.txStore.removeTTLIndex.size())
		ast.Equal(1, pool.txStore.parkingLotIndex.size())

		tx3 := constructTx(s, 3)
		err = pool.AddLocalTx(tx3)
		ast.NotNil(err)
		ast.Nil(pool.GetPendingTxByHash(tx2.RbftGetTxHash()), "tx2 is not exist in txpool")
		ast.Equal(1, pool.txStore.localTTLIndex.size())
		ast.Equal(1, pool.txStore.removeTTLIndex.size())
		ast.Equal(0, pool.txStore.parkingLotIndex.size())
	})
}
func TestTxPoolImpl_AddRemoteTxs(t *testing.T) {
	t.Parallel()
	t.Run("nonce is wanted", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		from := s.Addr.String()
		tx := constructTx(s, 0)
		pool.AddRemoteTxs([]*types.Transaction{tx})
		// because add remote txs is async,
		// so we need to send getPendingTxByHash event to ensure last event is handled
		ast.Equal(tx.RbftGetTxHash(), pool.GetPendingTxByHash(tx.RbftGetTxHash()).RbftGetTxHash())
		ast.NotNil(pool.txStore.allTxs[from])
		ast.Equal(1, len(pool.txStore.allTxs[from].items))
		ast.Equal(1, len(pool.txStore.txHashMap))
		ast.Equal(uint64(0), pool.txStore.txHashMap[tx.RbftGetTxHash()].nonce)
		ast.Equal(uint64(1), pool.txStore.priorityNonBatchSize)
		ast.Equal(1, pool.txStore.priorityIndex.size())
		ast.Equal(0, pool.txStore.parkingLotIndex.size())
		poolTx := pool.txStore.allTxs[from].items[0]
		ast.Equal(tx.RbftGetTxHash(), poolTx.rawTx.RbftGetTxHash())
	})

	t.Run("pool is full", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		pool.poolMaxSize = 1
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)
		ast.False(pool.statusMgr.In(PoolFull))

		s, err := types.GenerateSigner()
		ast.Nil(err)
		tx := constructTx(s, 0)
		pool.AddRemoteTxs([]*types.Transaction{tx})
		// because add remote txs is async,
		// so we need to send getPendingTxByHash event to ensure last event is handled
		ast.Equal(tx.RbftGetTxHash(), pool.GetPendingTxByHash(tx.RbftGetTxHash()).RbftGetTxHash())
		ast.True(pool.statusMgr.In(PoolFull))

		tx1 := constructTx(s, 1)
		pool.AddRemoteTxs([]*types.Transaction{tx1})
		ast.Nil(pool.GetPendingTxByHash(tx1.RbftGetTxHash()), "tx1 is not exit in txpool, because pool is full")
	})

	t.Run("nonce is bigger in same txs", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		from := s.Addr.String()
		txs := constructTxs(s, 10)
		// remove tx5
		lackTx := txs[5]
		txs = append(txs[:5], txs[6:]...)

		pool.AddRemoteTxs(txs)
		// because add remote txs is async,
		// so we need to send getPendingTxByHash event to ensure last event is handled
		ast.Equal(txs[0].RbftGetTxHash(), pool.GetPendingTxByHash(txs[0].RbftGetTxHash()).RbftGetTxHash())
		ast.NotNil(pool.txStore.allTxs[from])
		ast.Equal(9, len(pool.txStore.allTxs[from].items))
		ast.Equal(9, len(pool.txStore.txHashMap))
		ast.Equal(uint64(5), pool.txStore.priorityNonBatchSize)
		ast.Equal(5, pool.txStore.priorityIndex.size(), "tx0-tx4 are in priority queue")
		ast.Equal(4, pool.txStore.parkingLotIndex.size(), "tx6-tx9 are in parking lot")
		ast.Equal(0, pool.txStore.localTTLIndex.size())
		ast.Equal(9, pool.txStore.removeTTLIndex.size())

		pool.AddRemoteTxs([]*types.Transaction{lackTx})
		// because add remote txs is async,
		// so we need to send getPendingTxByHash event to ensure last event is handled
		ast.Equal(lackTx.RbftGetTxHash(), pool.GetPendingTxByHash(lackTx.RbftGetTxHash()).RbftGetTxHash())
		ast.Equal(10, len(pool.txStore.allTxs[from].items))
		ast.Equal(10, len(pool.txStore.txHashMap))
		ast.Equal(uint64(10), pool.txStore.priorityNonBatchSize)
		ast.Equal(10, pool.txStore.priorityIndex.size(), "tx0-tx0 are in priority queue")
		ast.Equal(4, pool.txStore.parkingLotIndex.size(), "tx6-tx9 are in parking lot")
		ast.Equal(0, pool.txStore.localTTLIndex.size())
		ast.Equal(10, pool.txStore.removeTTLIndex.size())
	})

	t.Run("nonce is lower", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		from := s.Addr.String()
		txs := constructTxs(s, 10)
		pool.AddRemoteTxs(txs)
		// because add remote txs is async,
		// so we need to send getPendingTxByHash event to ensure last event is handled
		ast.Equal(txs[0].RbftGetTxHash(), pool.GetPendingTxByHash(txs[0].RbftGetTxHash()).RbftGetTxHash())

		ast.NotNil(pool.txStore.allTxs[from])
		ast.Equal(10, len(pool.txStore.allTxs[from].items))
		ast.Equal(10, len(pool.txStore.txHashMap))
		ast.Equal(uint64(10), pool.txStore.priorityNonBatchSize)
		ast.Equal(10, pool.txStore.priorityIndex.size())
		ast.Equal(0, pool.txStore.parkingLotIndex.size())

		lowTx := txs[0]
		pool.AddRemoteTxs([]*types.Transaction{lowTx})
		// because add remote txs is async,
		// so we need to send getPendingTxByHash event to ensure last event is handled
		ast.Equal(txs[0].RbftGetTxHash(), pool.GetPendingTxByHash(txs[0].RbftGetTxHash()).RbftGetTxHash())
		ast.Equal(10, len(pool.txStore.allTxs[from].items), "add remote tx failed because tx nonce is lower")
	})

	t.Run("duplicate tx", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		tx := constructTx(s, 0)
		err = pool.AddLocalTx(tx)
		ast.Nil(err)
		ast.Equal(1, pool.txStore.localTTLIndex.size())

		pool.AddRemoteTxs([]*types.Transaction{tx})
		ast.Equal(tx.RbftGetTxHash(), pool.GetPendingTxByHash(tx.RbftGetTxHash()).RbftGetTxHash())
		ast.Equal(1, pool.txStore.localTTLIndex.size(), "remote tx not replace it")

		highTx := constructTx(s, 10)
		err = pool.AddLocalTx(highTx)
		ast.Nil(err)
		ast.Equal(2, pool.txStore.localTTLIndex.size())

		pool.AddRemoteTxs([]*types.Transaction{highTx})
		ast.Equal(tx.RbftGetTxHash(), pool.GetPendingTxByHash(tx.RbftGetTxHash()).RbftGetTxHash())
		ast.Equal(2, pool.txStore.localTTLIndex.size(), "remote tx not replace it")
	})

	t.Run("duplicate nonce but not the same tx in different txs", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		from := s.Addr.String()
		ast.Nil(err)
		tx01 := constructTx(s, 0)
		err = pool.AddLocalTx(tx01)
		ast.Nil(err)
		ast.Equal(1, pool.txStore.localTTLIndex.size(), "tx01 exist in txpool")

		tx02, err := types.GenerateTransactionWithSigner(0, to, big.NewInt(1000), nil, s)
		ast.Nil(err)

		err = pool.AddLocalTx(tx02)
		ast.NotNil(err)
		ast.Contains(err.Error(), ErrNonceTooLow.Error())

		tx21 := constructTx(s, 2)
		err = pool.AddLocalTx(tx21)
		ast.Nil(err)
		poolTx := pool.txStore.getPoolTxByTxnPointer(from, tx21.RbftGetNonce())
		ast.Equal(tx21.RbftGetTxHash(), poolTx.rawTx.RbftGetTxHash(), "tx21 exist in txpool")
		ast.Equal(2, pool.txStore.localTTLIndex.size(), "tx21 exist in txpool")

		tx22, err := types.GenerateTransactionWithSigner(2, to, big.NewInt(1000), nil, s)
		ast.Nil(err)
		ast.NotEqual(tx21.RbftGetTxHash(), tx22.RbftGetTxHash())
		ast.Equal(tx21.RbftGetNonce(), tx22.RbftGetNonce())

		pool.AddRemoteTxs([]*types.Transaction{tx22})
		ast.Equal(tx22.RbftGetTxHash(), pool.GetPendingTxByHash(tx22.RbftGetTxHash()).RbftGetTxHash())

		poolTx = pool.txStore.getPoolTxByTxnPointer(from, tx22.RbftGetNonce())
		ast.Equal(tx22.RbftGetTxHash(), poolTx.rawTx.RbftGetTxHash(), "tx22 replaced tx21")
		ast.Equal(1, pool.txStore.localTTLIndex.size(), "tx21 removed in txpool")
	})

	t.Run("duplicate nonce but not the same tx in same txs", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		tx01 := constructTx(s, 0)
		tx02, err := types.GenerateTransactionWithSigner(0, to, big.NewInt(1000), nil, s)
		ast.Nil(err)

		txs := []*types.Transaction{tx01, tx02}
		pool.AddRemoteTxs(txs)
		ast.Equal(tx01.RbftGetTxHash(), pool.GetPendingTxByHash(tx01.RbftGetTxHash()).RbftGetTxHash())
		ast.Nil(pool.GetPendingTxByHash(tx02.RbftGetTxHash()), "tx02 not exist in txpool")
		ast.Equal(0, pool.txStore.localTTLIndex.size())
		ast.Equal(1, pool.txStore.removeTTLIndex.size())

		tx21 := constructTx(s, 2)
		tx22, err := types.GenerateTransactionWithSigner(2, to, big.NewInt(1000), nil, s)
		ast.Nil(err)
		txs = []*types.Transaction{tx21, tx22}
		pool.AddRemoteTxs(txs)
		ast.Nil(pool.GetPendingTxByHash(tx21.RbftGetTxHash()), "tx21 not exist in txpool")
		ast.Equal(tx22.RbftGetTxHash(), pool.GetPendingTxByHash(tx22.RbftGetTxHash()).RbftGetTxHash())
		ast.Equal(0, pool.txStore.localTTLIndex.size())
		ast.Equal(2, pool.txStore.removeTTLIndex.size())
	})
}

func TestTxPoolImpl_ReceiveMissingRequests(t *testing.T) {
	t.Parallel()
	t.Run("handle missing requests", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		// ensure not notify generate batch
		pool.batchSize = 500
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)

		txs := constructTxs(s, 4)
		txHashList := make([]string, len(txs))
		txsM := make(map[uint64]*types.Transaction)

		lo.ForEach(txs, func(tx *types.Transaction, index int) {
			txHashList[index] = tx.RbftGetTxHash()
			txsM[uint64(index)] = tx
		})
		newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
			TxList:     txs,
			LocalList:  []bool{true, true, true, true},
			TxHashList: txHashList,
			Timestamp:  time.Now().UnixNano(),
		}

		batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
		err = pool.ReceiveMissingRequests(batchDigest, txsM)
		ast.Nil(err)
		ast.Equal(0, len(pool.txStore.txHashMap), "missingBatch is nil")

		missingHashList := make(map[uint64]string)
		missingHashList[uint64(0)] = txsM[uint64(0)].RbftGetTxHash()
		pool.txStore.missingBatch[batchDigest] = missingHashList
		err = pool.ReceiveMissingRequests(batchDigest, txsM)
		ast.NotNil(err, "expect len is not equal to missingBatch len")

		missingHashList[uint64(1)] = txsM[uint64(1)].RbftGetTxHash()
		missingHashList[uint64(2)] = txsM[uint64(2)].RbftGetTxHash()
		missingHashList[uint64(3)] = "wrong_hash3"
		pool.txStore.missingBatch[batchDigest] = missingHashList
		err = pool.ReceiveMissingRequests(batchDigest, txsM)
		ast.NotNil(err, "find a hash mismatch tx")
		ast.Equal(1, len(pool.txStore.missingBatch))
		//insert right txHash
		missingHashList[uint64(3)] = txsM[uint64(3)].RbftGetTxHash()
		pool.txStore.missingBatch[batchDigest] = missingHashList
		err = pool.ReceiveMissingRequests(batchDigest, txsM)
		ast.Nil(err)
		ast.Equal(0, len(pool.txStore.missingBatch), "missingBatch had been removed")
	})

	t.Run("trigger notify findNextBatch, before receive missing requests from primary, receive from addNewRequests", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		// ensure not notify generate batch
		pool.batchSize = 500
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)

		txs := constructTxs(s, 4)
		txHashList := make([]string, len(txs))
		txsM := make(map[uint64]*types.Transaction)

		lo.ForEach(txs, func(tx *types.Transaction, index int) {
			txHashList[index] = tx.RbftGetTxHash()
			txsM[uint64(index)] = tx
		})
		newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
			TxList:     txs,
			LocalList:  []bool{true, true, true, true},
			TxHashList: txHashList,
			Timestamp:  time.Now().UnixNano(),
		}

		batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
		notifySignalCh := make(chan string, 1)
		pool.notifyFindNextBatchFn = func(hashes ...string) {
			notifySignalCh <- hashes[0]
		}
		s, err = types.GenerateSigner()
		ast.Nil(err)

		txs = constructTxs(s, 4)
		txHashList = make([]string, len(txs))
		txsM = make(map[uint64]*types.Transaction)
		missingHashList := make(map[uint64]string)

		lo.ForEach(txs, func(tx *types.Transaction, index int) {
			txHashList[index] = tx.RbftGetTxHash()
			txsM[uint64(index)] = tx
			missingHashList[uint64(index)] = tx.RbftGetTxHash()
		})
		newBatch = &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
			TxList:     txs,
			LocalList:  []bool{true, true, true, true},
			TxHashList: txHashList,
			Timestamp:  time.Now().UnixNano(),
		}

		batchDigest = getBatchHash[types.Transaction, *types.Transaction](newBatch)
		pool.txStore.missingBatch[batchDigest] = missingHashList
		pool.AddRemoteTxs(txs)
		ast.NotNil(pool.GetPendingTxByHash(txs[0].RbftGetTxHash()))
		completedDigest := <-notifySignalCh
		ast.Equal(batchDigest, completedDigest)
		ast.Equal(0, len(pool.txStore.missingBatch), "missingBatch had been removed")
	})

	t.Run("receive missing requests from primary, trigger replace tx", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		// ensure not notify generate batch
		pool.batchSize = 500
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)

		oldTx0, err := types.GenerateTransactionWithSigner(0, to, big.NewInt(1000), nil, s)
		ast.Nil(err)

		// insert oldTx0
		err = pool.AddLocalTx(oldTx0)
		ast.Nil(err)
		ast.NotNil(pool.GetPendingTxByHash(oldTx0.RbftGetTxHash()))

		txs := constructTxs(s, 4)
		newTx0 := txs[0]
		ast.NotEqual(oldTx0.RbftGetTxHash(), newTx0.RbftGetTxHash())
		ast.Equal(oldTx0.RbftGetNonce(), newTx0.RbftGetNonce())

		txHashList := make([]string, len(txs))
		txsM := make(map[uint64]*types.Transaction)
		missingHashList := make(map[uint64]string)

		lo.ForEach(txs, func(tx *types.Transaction, index int) {
			txHashList[index] = tx.RbftGetTxHash()
			txsM[uint64(index)] = tx
			missingHashList[uint64(index)] = tx.RbftGetTxHash()
		})
		newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
			TxList:     txs,
			LocalList:  []bool{true, true, true, true},
			TxHashList: txHashList,
			Timestamp:  time.Now().UnixNano(),
		}

		batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
		pool.txStore.missingBatch[batchDigest] = missingHashList

		err = pool.ReceiveMissingRequests(batchDigest, txsM)
		ast.Nil(err)
		ast.Nil(pool.GetPendingTxByHash(oldTx0.RbftGetTxHash()))
		ast.NotNil(pool.GetPendingTxByHash(newTx0.RbftGetTxHash()))
		ast.Equal(0, pool.txStore.localTTLIndex.size())
		ast.Equal(0, len(pool.txStore.missingBatch), "missingBatch had been removed")
	})

	t.Run("receive missing requests from primary, but pool full", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		// ensure not notify generate batch
		pool.batchSize = 500
		pool.poolMaxSize = 1
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)

		tx, err := types.GenerateTransactionWithSigner(0, to, big.NewInt(1000), nil, s)
		ast.Nil(err)

		// insert tx to trigger pool full
		err = pool.AddLocalTx(tx)
		ast.Nil(err)
		ast.NotNil(pool.GetPendingTxByHash(tx.RbftGetTxHash()))
		ast.True(pool.IsPoolFull())

		txs := constructTxs(s, 4)

		txHashList := make([]string, len(txs))
		txsM := make(map[uint64]*types.Transaction)
		missingHashList := make(map[uint64]string)

		lo.ForEach(txs, func(tx *types.Transaction, index int) {
			txHashList[index] = tx.RbftGetTxHash()
			txsM[uint64(index)] = tx
			missingHashList[uint64(index)] = tx.RbftGetTxHash()
		})
		newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
			TxList:     txs,
			LocalList:  []bool{true, true, true, true},
			TxHashList: txHashList,
			Timestamp:  time.Now().UnixNano(),
		}

		batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
		pool.txStore.missingBatch[batchDigest] = missingHashList

		err = pool.ReceiveMissingRequests(batchDigest, txsM)
		ast.NotNil(err)
		ast.Contains(err.Error(), ErrTxPoolFull.Error())
	})
}

func TestTxPoolImpl_GenerateRequestBatch(t *testing.T) {
	t.Parallel()
	t.Run("generate wrong batch event", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		wrongTyp := 1000
		_, err = pool.GenerateRequestBatch(wrongTyp)
		ast.NotNil(err)
	})

	t.Run("generate batch size event", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		ch := make(chan int, 1)
		pool.notifyGenerateBatchFn = func(typ int) {
			ch <- typ
		}
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		txs := constructTxs(s, 8)
		// remove tx1
		txs = append(txs[:1], txs[2:]...)
		pool.AddRemoteTxs(txs)
		tx1, err := types.GenerateTransactionWithSigner(1, to, big.NewInt(1000), nil, s)
		ast.Nil(err)
		err = pool.AddLocalTx(tx1)
		ast.Nil(err)
		typ := <-ch
		ast.Equal(txpool.GenBatchSizeEvent, typ)
		batch, err := pool.GenerateRequestBatch(typ)
		ast.Nil(err)
		ast.NotNil(batch)
		ast.Equal(uint64(4), pool.txStore.priorityNonBatchSize)
	})

	t.Run("generate batch size event which is less than batchSize", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		err := pool.Start()
		defer pool.Stop()
		ast.Nil(err)

		s, err := types.GenerateSigner()
		ast.Nil(err)
		txs := constructTxs(s, 3)
		pool.AddRemoteTxs(txs)
		_, err = pool.GenerateRequestBatch(txpool.GenBatchSizeEvent)
		ast.NotNil(err)
		ast.Contains(err.Error(), "ignore generate batch")
	})

	t.Run("generate batch timeout event", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		s, err := types.GenerateSigner()
		ast.Nil(err)
		tx := constructTx(s, 0)
		err = pool.AddLocalTx(tx)
		ast.Nil(err)

		batchTimer := timer.NewTimerManager(pool.logger)
		ch := make(chan *txpool.RequestHashBatch[types.Transaction, *types.Transaction], 1)
		handler := func(name timer.TimeoutEvent) {
			batch, err := pool.generateRequestBatch(txpool.GenBatchTimeoutEvent)
			ast.Nil(err)
			ch <- batch
		}
		err = batchTimer.CreateTimer(timer.Batch, 1*time.Millisecond, handler)
		ast.Nil(err)
		err = batchTimer.StartTimer(timer.Batch)
		ast.Nil(err)

		batch := <-ch
		ast.Equal(1, len(batch.TxList))
		ast.Equal(tx.RbftGetTxHash(), batch.TxHashList[0])

	})

	t.Run("generate batch timeout event which tx pool is empty", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		batchTimer := timer.NewTimerManager(pool.logger)
		ch := make(chan *txpool.RequestHashBatch[types.Transaction, *types.Transaction], 1)
		handler := func(name timer.TimeoutEvent) {
			batch, err := pool.generateRequestBatch(txpool.GenBatchTimeoutEvent)
			ast.NotNil(err)
			ast.Contains(err.Error(), "there is no pending tx, ignore generate batch")
			ch <- batch
		}
		err = batchTimer.CreateTimer(timer.Batch, 1*time.Millisecond, handler)
		ast.Nil(err)
		err = batchTimer.StartTimer(timer.Batch)
		ast.Nil(err)

		batch := <-ch
		ast.Nil(batch)

	})

	t.Run("generate no-tx batch timeout event which tx pool is not empty", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		s, err := types.GenerateSigner()
		ast.Nil(err)
		tx := constructTx(s, 0)
		err = pool.AddLocalTx(tx)
		ast.Nil(err)

		batch, err := pool.GenerateRequestBatch(txpool.GenBatchNoTxTimeoutEvent)
		ast.NotNil(err)
		ast.Contains(err.Error(), "there is pending tx, ignore generate no tx batch")
		ast.Nil(batch)
	})

	t.Run("generate no-tx batch timeout event which not support no-tx", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		_, err = pool.GenerateRequestBatch(txpool.GenBatchNoTxTimeoutEvent)
		ast.NotNil(err)
		ast.Contains(err.Error(), "not supported generate no tx batch")
	})

	t.Run("generate no-tx batch timeout event", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.batchSize = 4
		pool.isTimed = true
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		noTxBatchTimer := timer.NewTimerManager(pool.logger)
		ch := make(chan *txpool.RequestHashBatch[types.Transaction, *types.Transaction], 1)
		handler := func(name timer.TimeoutEvent) {
			batch, err := pool.generateRequestBatch(txpool.GenBatchNoTxTimeoutEvent)
			ast.Nil(err)
			ch <- batch
		}
		err = noTxBatchTimer.CreateTimer(timer.NoTxBatch, 2*time.Millisecond, handler)
		ast.Nil(err)
		err = noTxBatchTimer.StartTimer(timer.NoTxBatch)
		ast.Nil(err)

		batch := <-ch
		ast.NotNil(batch)
		ast.Equal(0, len(batch.TxList))
	})
}

func TestTxPoolImpl_ReConstructBatchByOrder(t *testing.T) {
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	err := pool.Start()
	ast.Nil(err)
	defer pool.Stop()

	s, err := types.GenerateSigner()
	ast.Nil(err)

	txs := constructTxs(s, 4)
	txHashList := make([]string, len(txs))
	txsM := make(map[uint64]*types.Transaction)

	lo.ForEach(txs, func(tx *types.Transaction, index int) {
		txHashList[index] = tx.RbftGetTxHash()
		txsM[uint64(index)] = tx
	})
	newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
		TxList:     txs,
		LocalList:  []bool{true, true, true, true},
		TxHashList: txHashList,
		Timestamp:  time.Now().UnixNano(),
	}

	batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
	newBatch.BatchHash = batchDigest

	pool.txStore.batchesCache[batchDigest] = newBatch

	duHash, err := pool.ReConstructBatchByOrder(newBatch)
	ast.NotNil(err)
	ast.Contains(err.Error(), "batch already exist")
	ast.Equal(0, len(duHash))

	illegalBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
		TxList:     txs,
		LocalList:  []bool{true, true, true, true},
		TxHashList: nil,
		Timestamp:  time.Now().UnixNano(),
	}

	_, err = pool.ReConstructBatchByOrder(illegalBatch)
	ast.NotNil(err)
	ast.Contains(err.Error(), "TxHashList and TxList have different lengths")

	illegalTxHashList := make([]string, len(txs))
	lo.ForEach(txHashList, func(tx string, index int) {
		if index == 1 {
			illegalTxHashList[index] = "invalid"
		} else {
			illegalTxHashList[index] = tx
		}
	})
	illegalBatch.TxHashList = illegalTxHashList

	_, err = pool.ReConstructBatchByOrder(illegalBatch)
	ast.NotNil(err)
	ast.Contains(err.Error(), "hash of transaction does not match")

	illegalBatch.TxHashList = txHashList
	illegalBatch.BatchHash = "invalid"
	_, err = pool.ReConstructBatchByOrder(illegalBatch)
	ast.NotNil(err)
	ast.Contains(err.Error(), "batch hash does not match")

	pool.txStore.batchesCache = make(map[string]*txpool.RequestHashBatch[types.Transaction, *types.Transaction])
	duTxs := txs[:2]
	pointer0 := &txPointer{
		account: duTxs[0].RbftGetFrom(),
		nonce:   duTxs[0].RbftGetNonce(),
	}
	pointer1 := &txPointer{
		account: duTxs[1].RbftGetFrom(),
		nonce:   duTxs[1].RbftGetNonce(),
	}
	pool.txStore.batchedTxs[*pointer0] = true
	pool.txStore.batchedTxs[*pointer1] = true

	dupTxHashes, err := pool.ReConstructBatchByOrder(newBatch)
	ast.Nil(err)
	ast.Equal(2, len(dupTxHashes))
	ast.Equal(duTxs[0].RbftGetTxHash(), dupTxHashes[0])
	ast.Equal(duTxs[1].RbftGetTxHash(), dupTxHashes[1])
}

func TestTxPoolImpl_GetRequestsByHashList(t *testing.T) {
	t.Parallel()
	t.Run("exist batch or missingTxs", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		s, err := types.GenerateSigner()
		ast.Nil(err)

		txs := constructTxs(s, 4)
		txHashList := make([]string, len(txs))
		txsM := make(map[uint64]*types.Transaction)

		lo.ForEach(txs, func(tx *types.Transaction, index int) {
			txHashList[index] = tx.RbftGetTxHash()
			txsM[uint64(index)] = tx
		})
		newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
			TxList:     txs,
			LocalList:  []bool{true, true, true, true},
			TxHashList: txHashList,
			Timestamp:  time.Now().UnixNano(),
		}

		batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
		newBatch.BatchHash = batchDigest

		pool.txStore.batchesCache[batchDigest] = newBatch
		getTxs, localList, missingTxsHash, err := pool.GetRequestsByHashList(batchDigest, newBatch.Timestamp, newBatch.TxHashList, nil)
		ast.Nil(err)
		ast.Equal(4, len(getTxs))
		ast.Equal(4, len(localList))
		ast.Equal(0, len(missingTxsHash))

		pool.txStore.batchesCache = make(map[string]*txpool.RequestHashBatch[types.Transaction, *types.Transaction])
		expectMissingTxsHash := make(map[uint64]string)
		expectMissingTxsHash[0] = txs[0].RbftGetTxHash()
		pool.txStore.missingBatch[batchDigest] = expectMissingTxsHash

		getTxs, localList, missingTxsHash, err = pool.GetRequestsByHashList(batchDigest, newBatch.Timestamp, newBatch.TxHashList, nil)
		ast.Nil(err)
		ast.Equal(0, len(getTxs))
		ast.Equal(0, len(localList))
		ast.Equal(1, len(missingTxsHash))
		ast.Equal(txs[0].RbftGetTxHash(), missingTxsHash[0])
	})

	t.Run("doesn't exist tx in txpool", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		s, err := types.GenerateSigner()
		ast.Nil(err)

		txs := constructTxs(s, 4)
		txHashList := make([]string, len(txs))
		txsM := make(map[uint64]*types.Transaction)

		lo.ForEach(txs, func(tx *types.Transaction, index int) {
			txHashList[index] = tx.RbftGetTxHash()
			txsM[uint64(index)] = tx
		})
		newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
			TxList:     txs,
			LocalList:  []bool{true, true, true, true},
			TxHashList: txHashList,
			Timestamp:  time.Now().UnixNano(),
		}

		batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
		newBatch.BatchHash = batchDigest

		getTxs, localList, missingTxsHash, err := pool.GetRequestsByHashList(batchDigest, newBatch.Timestamp, newBatch.TxHashList, nil)
		ast.Nil(err)
		ast.Equal(0, len(getTxs))
		ast.Equal(0, len(localList))
		ast.Equal(4, len(missingTxsHash))

	})

	t.Run("exist duplicate tx in txpool", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		s, err := types.GenerateSigner()
		ast.Nil(err)

		txs := constructTxs(s, 4)
		txHashList := make([]string, len(txs))
		txsM := make(map[uint64]*types.Transaction)

		lo.ForEach(txs, func(tx *types.Transaction, index int) {
			txHashList[index] = tx.RbftGetTxHash()
			txsM[uint64(index)] = tx
		})
		newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
			TxList:     txs,
			LocalList:  []bool{true, true, true, true},
			TxHashList: txHashList,
			Timestamp:  time.Now().UnixNano(),
		}

		batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
		newBatch.BatchHash = batchDigest

		pool.AddRemoteTxs(txs)
		dupTx := txs[0]
		pointer := &txPointer{
			account: dupTx.RbftGetFrom(),
			nonce:   dupTx.RbftGetNonce(),
		}
		pool.txStore.batchedTxs[*pointer] = true
		_, _, _, err = pool.GetRequestsByHashList(batchDigest, newBatch.Timestamp, newBatch.TxHashList, nil)
		ast.NotNil(err)
		ast.Contains(err.Error(), "duplicate transaction")

		ast.Equal(uint64(4), pool.txStore.priorityNonBatchSize)
		getTxs, localList, missing, err := pool.GetRequestsByHashList(batchDigest, newBatch.Timestamp, newBatch.TxHashList, []string{dupTx.RbftGetTxHash()})
		ast.Nil(err)
		ast.Equal(0, len(missing))
		ast.Equal(4, len(getTxs))
		ast.Equal(4, len(localList))
		ast.Equal(uint64(0), pool.txStore.priorityNonBatchSize)

		lo.ForEach(localList, func(local bool, index int) {
			ast.False(local)
		})
	})
}

func TestTxPoolImpl_SendMissingRequests(t *testing.T) {
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	err := pool.Start()
	ast.Nil(err)
	defer pool.Stop()
	s, err := types.GenerateSigner()
	ast.Nil(err)

	txs := constructTxs(s, 4)
	txHashList := make([]string, len(txs))
	txsM := make(map[uint64]*types.Transaction)

	lo.ForEach(txs, func(tx *types.Transaction, index int) {
		txHashList[index] = tx.RbftGetTxHash()
		txsM[uint64(index)] = tx
	})
	newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
		TxList:     txs,
		LocalList:  []bool{true, true, true, true},
		TxHashList: txHashList,
		Timestamp:  time.Now().UnixNano(),
	}

	batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
	newBatch.BatchHash = batchDigest

	missHashList := make(map[uint64]string)
	missHashList[uint64(0)] = txHashList[0]
	_, err = pool.SendMissingRequests(batchDigest, missHashList)
	ast.NotNil(err)
	ast.Contains(err.Error(), "doesn't exist in txHashMap")

	pool.AddRemoteTxs(txs)
	_, err = pool.SendMissingRequests(batchDigest, missHashList)
	ast.NotNil(err)
	ast.Contains(err.Error(), "doesn't exist in batchedCache")

	pool.txStore.batchesCache[batchDigest] = newBatch

	illegalMissHashList := make(map[uint64]string)
	illegalMissHashList[uint64(4)] = txHashList[0]

	_, err = pool.SendMissingRequests(batchDigest, illegalMissHashList)
	ast.NotNil(err)
	ast.Contains(err.Error(), "find invalid transaction")

	getTxs, err := pool.SendMissingRequests(batchDigest, missHashList)
	ast.Nil(err)
	ast.Equal(1, len(getTxs))
	ast.Equal(txs[0].RbftGetTxHash(), getTxs[0].RbftGetTxHash())
}

func TestTxPoolImpl_FilterOutOfDateRequests(t *testing.T) {
	t.Parallel()
	t.Run("filter out of date requests with timeout", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		pool.toleranceTime = 1 * time.Millisecond
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		s, err := types.GenerateSigner()
		ast.Nil(err)
		from := s.Addr.String()
		tx0 := constructTx(s, 0)
		err = pool.AddLocalTx(tx0)
		ast.Nil(err)
		poolTx := pool.txStore.getPoolTxByTxnPointer(from, 0)
		ast.NotNil(poolTx)
		firstTime := poolTx.lifeTime

		// trigger update lifeTime
		time.Sleep(2 * time.Millisecond)
		tx1 := constructTx(s, 1)
		err = pool.AddLocalTx(tx1)
		ast.Nil(err)
		txs := pool.FilterOutOfDateRequests(true)
		ast.True(len(txs) >= 1)
		ast.True(poolTx.lifeTime > firstTime)
	})

	t.Run("filter out of date requests without timeout", func(t *testing.T) {
		ast := assert.New(t)
		pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
		// ensure that not triggering timeout
		pool.toleranceTime = 100 * time.Second
		err := pool.Start()
		ast.Nil(err)
		defer pool.Stop()

		s, err := types.GenerateSigner()
		ast.Nil(err)
		from := s.Addr.String()
		tx0 := constructTx(s, 0)
		err = pool.AddLocalTx(tx0)
		ast.Nil(err)
		poolTx := pool.txStore.getPoolTxByTxnPointer(from, 0)
		ast.NotNil(poolTx)
		firstTime := poolTx.lifeTime
		// sleep a while to trigger update lifeTime
		time.Sleep(1 * time.Millisecond)

		txs := pool.FilterOutOfDateRequests(true)
		ast.Equal(0, len(txs))

		txs = pool.FilterOutOfDateRequests(false)
		ast.Equal(1, len(txs))
		ast.True(poolTx.lifeTime > firstTime)
	})

}

func TestTxPoolImpl_RestoreOneBatch(t *testing.T) {
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	err := pool.Start()
	ast.Nil(err)
	defer pool.Stop()

	err = pool.RestoreOneBatch("wrong batch")
	ast.NotNil(err)
	ast.Contains(err.Error(), "can't find batch from batchesCache")

	s, err := types.GenerateSigner()
	ast.Nil(err)
	from := s.Addr.String()
	txs := constructTxs(s, 4)
	txHashList := make([]string, len(txs))
	txsM := make(map[uint64]*types.Transaction)

	lo.ForEach(txs, func(tx *types.Transaction, index int) {
		txHashList[index] = tx.RbftGetTxHash()
		txsM[uint64(index)] = tx
	})
	newBatch := &txpool.RequestHashBatch[types.Transaction, *types.Transaction]{
		TxList:     txs,
		LocalList:  []bool{true, true, true, true},
		TxHashList: txHashList,
		Timestamp:  time.Now().UnixNano(),
	}

	batchDigest := getBatchHash[types.Transaction, *types.Transaction](newBatch)
	newBatch.BatchHash = batchDigest

	pool.txStore.batchesCache[batchDigest] = newBatch

	err = pool.RestoreOneBatch(batchDigest)
	ast.NotNil(err)
	ast.Contains(err.Error(), "can't find tx from txHashMap")

	pool.AddRemoteTxs(txs)
	ast.NotNil(pool.GetPendingTxByHash(txHashList[0]))
	removeTx := pool.txStore.getPoolTxByTxnPointer(from, 0)
	pool.txStore.priorityIndex.removeByOrderedQueueKey(removeTx)

	err = pool.RestoreOneBatch(batchDigest)
	ast.NotNil(err)
	ast.Contains(err.Error(), "can't find tx from priorityIndex")

	pool.txStore.priorityIndex.insertByOrderedQueueKey(removeTx)
	err = pool.RestoreOneBatch(batchDigest)
	ast.NotNil(err)
	ast.Contains(err.Error(), "can't find tx from batchedTxs")

	ast.Equal(uint64(4), pool.txStore.priorityNonBatchSize)
	_, err = pool.GenerateRequestBatch(txpool.GenBatchTimeoutEvent)
	ast.Nil(err)
	ast.Equal(uint64(0), pool.txStore.priorityNonBatchSize)

	err = pool.RestoreOneBatch(batchDigest)
	ast.Nil(err)
	ast.Equal(uint64(4), pool.txStore.priorityNonBatchSize)
}

func TestTxPoolImpl_RestorePool(t *testing.T) {
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	err := pool.Start()
	ast.Nil(err)
	defer pool.Stop()

	s, err := types.GenerateSigner()
	ast.Nil(err)
	txs := constructTxs(s, 3)
	pool.AddRemoteTxs(txs)
	tx3 := constructTx(s, 3)
	err = pool.AddLocalTx(tx3)
	ast.Nil(err)

	batch, err := pool.GenerateRequestBatch(txpool.GenBatchTimeoutEvent)
	ast.Nil(err)
	ast.Equal(4, len(batch.TxList))
	ast.Equal(uint64(0), pool.txStore.priorityNonBatchSize)
	ast.Equal(4, len(pool.txStore.batchedTxs))
	ast.Equal(1, len(pool.txStore.batchesCache))
	ast.Equal(uint64(4), pool.txStore.nonceCache.pendingNonces[tx3.RbftGetFrom()])
	ast.Equal(batch.Timestamp, pool.txStore.batchesCache[batch.BatchHash].Timestamp)

	pool.RestorePool()
	ast.NotNil(pool.GetPendingTxByHash(tx3.RbftGetTxHash()))
	ast.Equal(uint64(4), pool.txStore.priorityNonBatchSize)
	ast.Equal(0, len(pool.txStore.batchedTxs))
	ast.Equal(0, len(pool.txStore.batchesCache))
	ast.Equal(uint64(0), pool.txStore.nonceCache.pendingNonces[tx3.RbftGetFrom()])
}

func TestTxPoolImpl_GetInfo(t *testing.T) {
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	err := pool.Start()
	ast.Nil(err)
	defer pool.Stop()

	s, err := types.GenerateSigner()
	ast.Nil(err)
	from := s.Addr.String()

	txs := constructTxs(s, 4)
	pool.AddRemoteTxs(txs)

	t.Run("getmeta", func(t *testing.T) {
		meta := pool.GetMeta(false)
		ast.NotNil(meta)
		ast.Equal(1, len(meta.Accounts))
		ast.NotNil(meta.Accounts[from])
		ast.Equal(uint64(4), meta.Accounts[from].PendingNonce)
		ast.Equal(uint64(0), meta.Accounts[from].CommitNonce)
		ast.Equal(uint64(4), meta.Accounts[from].TxCount)
		ast.Equal(4, len(meta.Accounts[from].SimpleTxs))
		ast.Equal(0, len(meta.Accounts[from].Txs))

		meta = pool.GetMeta(true)
		ast.Equal(0, len(meta.Accounts[from].SimpleTxs))
		ast.Equal(4, len(meta.Accounts[from].Txs))
	})

	t.Run("getAccountMeta", func(t *testing.T) {
		accountMeta := pool.GetAccountMeta(from, false)
		ast.NotNil(accountMeta)
		ast.Equal(uint64(4), accountMeta.PendingNonce)
		ast.Equal(uint64(0), accountMeta.CommitNonce)
		ast.Equal(uint64(4), accountMeta.TxCount)
		ast.Equal(4, len(accountMeta.SimpleTxs))
		ast.Equal(0, len(accountMeta.Txs))

		accountMeta = pool.GetAccountMeta(from, true)
		ast.NotNil(accountMeta)
		ast.Equal(0, len(accountMeta.SimpleTxs))
		ast.Equal(4, len(accountMeta.Txs))
	})

	t.Run("getTx", func(t *testing.T) {
		tx := pool.GetPendingTxByHash(txs[1].RbftGetTxHash())
		ast.Equal(txs[1].RbftGetTxHash(), tx.RbftGetTxHash())
	})

	t.Run("getNonce", func(t *testing.T) {
		nonce := pool.GetPendingTxCountByAccount(from)
		ast.Equal(uint64(4), nonce)
	})

	t.Run("get total tx count", func(t *testing.T) {
		count := pool.GetTotalPendingTxCount()
		ast.Equal(uint64(4), count)
	})

}

func TestTxPoolImpl_RemoveBatches(t *testing.T) {
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	pool.batchSize = 4
	err := pool.Start()
	defer pool.Stop()
	ast.Nil(err)

	pool.RemoveBatches([]string{"wrong batch"})

	s, err := types.GenerateSigner()
	ast.Nil(err)
	from := s.Addr.String()
	txs := constructTxs(s, 4)
	pool.AddRemoteTxs(txs)
	batch, err := pool.GenerateRequestBatch(txpool.GenBatchTimeoutEvent)
	ast.Nil(err)
	ast.Equal(4, len(pool.txStore.txHashMap))
	ast.Equal(4, len(batch.TxList))
	ast.Equal(uint64(0), pool.txStore.priorityNonBatchSize)
	ast.Equal(4, len(pool.txStore.batchedTxs))
	ast.NotNil(pool.txStore.batchesCache[batch.BatchHash])
	ast.Equal(uint64(4), pool.txStore.nonceCache.pendingNonces[from])
	ast.Equal(uint64(0), pool.txStore.nonceCache.commitNonces[from])

	pool.RemoveBatches([]string{batch.BatchHash})
	ast.Equal(uint64(4), pool.GetPendingTxCountByAccount(from))
	ast.Equal(0, len(pool.txStore.txHashMap))
	ast.Equal(uint64(4), pool.txStore.nonceCache.commitNonces[from])
}

func TestTxPoolImpl_RemoveStateUpdatingTxs(t *testing.T) {
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	err := pool.Start()
	defer pool.Stop()
	ast.Nil(err)

	s, err := types.GenerateSigner()
	ast.Nil(err)
	from := s.Addr.String()
	txs := constructTxs(s, 4)
	pool.AddRemoteTxs(txs)
	ast.Equal(uint64(4), pool.GetPendingTxCountByAccount(from))
	ast.Equal(4, len(pool.txStore.txHashMap))
	ast.Equal(uint64(4), pool.txStore.priorityNonBatchSize)
	ast.Equal(uint64(0), pool.txStore.nonceCache.commitNonces[from])

	txHashList := make([]string, len(txs))
	lo.ForEach(txs, func(tx *types.Transaction, index int) {
		txHashList[index] = tx.RbftGetTxHash()
	})
	pool.RemoveStateUpdatingTxs(txHashList)
	ast.Equal(uint64(4), pool.GetPendingTxCountByAccount(from))
	ast.Equal(0, len(pool.txStore.txHashMap))
	ast.Equal(uint64(0), pool.txStore.priorityNonBatchSize)
	ast.Equal(uint64(4), pool.txStore.nonceCache.commitNonces[from])
}

// nolint
func TestTPSWithLocalTx(t *testing.T) {
	t.Skip()
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	pool.batchSize = 500

	batchesCache := make(chan *txpool.RequestHashBatch[types.Transaction, *types.Transaction], 10240)
	pool.notifyGenerateBatchFn = func(typ int) {
		go func() {
			batch, err := pool.GenerateRequestBatch(typ)
			ast.Nil(err)
			ast.NotNil(batch)
			batchesCache <- batch
		}()
	}
	err := pool.Start()
	defer pool.Stop()
	ast.Nil(err)

	round := 5000
	thread := 100
	total := round * thread

	endCh := make(chan int64, 1)
	go listenBatchCache(total, endCh, batchesCache, pool, t)

	wg := new(sync.WaitGroup)
	start := time.Now().UnixNano()
	for i := 0; i < thread; i++ {
		wg.Add(1)
		s, err := types.GenerateSigner()
		ast.Nil(err)
		go func(i int, wg *sync.WaitGroup, s *types.Signer) {
			defer wg.Done()
			for j := 0; j < round; j++ {
				tx := constructTx(s, uint64(j))
				err = pool.AddLocalTx(tx)
				ast.Nil(err)
			}
		}(i, wg, s)
	}
	wg.Wait()

	end := <-endCh
	dur := end - start
	fmt.Printf("=========================duration: %v\n", time.Duration(dur).Seconds())
	fmt.Printf("=========================tps: %v\n", float64(total)/time.Duration(dur).Seconds())
}

// nolint
func TestTPSWithRemoteTxs(t *testing.T) {
	t.Skip()
	ast := assert.New(t)
	pool := mockTxPoolImpl[types.Transaction, *types.Transaction](t)
	pool.batchSize = 500
	pool.toleranceNonceGap = 100000

	batchesCache := make(chan *txpool.RequestHashBatch[types.Transaction, *types.Transaction], 10240)
	pool.notifyGenerateBatchFn = func(typ int) {
		go func() {
			batch, err := pool.GenerateRequestBatch(typ)
			ast.Nil(err)
			ast.NotNil(batch)
			batchesCache <- batch
		}()
	}
	err := pool.Start()
	defer pool.Stop()
	ast.Nil(err)

	round := 5000
	thread := 100
	total := round * thread

	endCh := make(chan int64, 1)
	txC := &txCache{
		RecvTxC: make(chan *types.Transaction, 10240),
		TxSetC:  make(chan []*types.Transaction, 1024),
		closeC:  make(chan struct{}),
		txSet:   make([]*types.Transaction, 0),
	}
	go listenBatchCache(total, endCh, batchesCache, pool, t)
	go txC.listenTxCache()
	// !!!NOTICE!!! if modify thread, need to modify postTxs' sleep timeout
	go txC.listenPostTxs(pool)

	start := time.Now().UnixNano()
	go prepareTx(thread, round, txC, t)

	end := <-endCh
	dur := end - start
	fmt.Printf("=========================duration: %v\n", time.Duration(dur).Seconds())
	fmt.Printf("=========================tps: %v\n", float64(total)/time.Duration(dur).Seconds())
}

func prepareTx(thread, round int, tc *txCache, t *testing.T) {
	wg := new(sync.WaitGroup)
	for i := 0; i < thread; i++ {
		wg.Add(1)
		s, err := types.GenerateSigner()
		require.Nil(t, err)
		go func(i int, wg *sync.WaitGroup, s *types.Signer) {
			defer wg.Done()
			for j := 0; j < round; j++ {
				tx := constructTx(s, uint64(j))
				tc.RecvTxC <- tx
			}
		}(i, wg, s)
	}
	wg.Wait()

}

type txCache struct {
	TxSetC  chan []*types.Transaction
	RecvTxC chan *types.Transaction
	closeC  chan struct{}
	txSet   []*types.Transaction
}

func (tc *txCache) listenTxCache() {
	for {
		select {
		case <-tc.closeC:
			return
		case tx := <-tc.RecvTxC:
			tc.txSet = append(tc.txSet, tx)
			if uint64(len(tc.txSet)) >= 50 {
				dst := make([]*types.Transaction, len(tc.txSet))
				copy(dst, tc.txSet)
				tc.TxSetC <- dst
				tc.txSet = make([]*types.Transaction, 0)
			}
		}
	}
}

func (tc *txCache) listenPostTxs(pool *txPoolImpl[types.Transaction, *types.Transaction]) {
	for {
		select {
		case <-tc.closeC:
			return
		case txs := <-tc.TxSetC:
			pool.AddRemoteTxs(txs)
			time.Sleep(3 * time.Millisecond)
		}
	}
}
func listenBatchCache(total int, endCh chan int64, cacheCh chan *txpool.RequestHashBatch[types.Transaction, *types.Transaction],
	pool *txPoolImpl[types.Transaction, *types.Transaction], t *testing.T) {

	seqNo := uint64(0)
	for {
		select {
		case batch := <-cacheCh:
			now := time.Now()
			txList, localList, missingTxs, err := pool.GetRequestsByHashList(batch.BatchHash, batch.Timestamp, batch.TxHashList, nil)
			require.Nil(t, err)
			require.Equal(t, len(batch.TxHashList), len(txList))
			require.Equal(t, len(batch.TxHashList), len(localList))
			require.Equal(t, 0, len(missingTxs))

			newBatch := &rbft.RequestBatch[types.Transaction, *types.Transaction]{
				RequestHashList: batch.TxHashList,
				RequestList:     txList,
				Timestamp:       batch.Timestamp,
				SeqNo:           atomic.AddUint64(&seqNo, 1),
				LocalList:       localList,
				BatchHash:       batch.BatchHash,
				Proposer:        1,
			}
			fmt.Printf("GetRequestsByHashList: %d, cost: %v\n", newBatch.SeqNo, time.Since(now))

			pool.RemoveBatches([]string{newBatch.BatchHash})
			if newBatch.SeqNo >= uint64(total)/pool.batchSize {
				end := newBatch.Timestamp
				endCh <- end
			}
			fmt.Printf("remove batch: %d, cost: %v\n", newBatch.SeqNo, time.Since(now))
		}
	}
}
