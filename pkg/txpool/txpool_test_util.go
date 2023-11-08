package txpool

import (
	"math/big"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/axiomesh/axiom-kit/types"

	"github.com/axiomesh/axiom-bft/common/consensus"
	log2 "github.com/axiomesh/axiom-kit/log"
)

// nolint
const (
	DefaultTestBatchSize = uint64(4)
)

// nolint
var to = types.NewAddressByStr("0xa2f28344131970356c4a112d1e634e51589aa57c")

// nolint
func mockTxPoolImpl[T any, Constraint consensus.TXConstraint[T]](t *testing.T) *txPoolImpl[T, Constraint] {
	ast := assert.New(t)
	pool, err := newTxPoolImpl[T, Constraint](NewMockTxPoolConfig())
	ast.Nil(err)
	conf := ConsensusConfig{
		SelfID: 1,
		NotifyGenerateBatchFn: func(typ int) {
			// do nothing
		},
	}
	pool.Init(conf)
	return pool
}

// NewMockTxPoolConfig returns the default test config
func NewMockTxPoolConfig() Config {
	log := log2.NewWithModule("txpool")
	log.Logger.SetLevel(logrus.DebugLevel)
	poolConfig := Config{
		BatchSize:     DefaultTestBatchSize,
		PoolSize:      DefaultPoolSize,
		Logger:        log,
		ToleranceTime: DefaultToleranceTime,
		GetAccountNonce: func(address string) uint64 {
			return 0
		},
		IsTimed: false,
	}
	return poolConfig
}

// nolint
func constructTx(s *types.Signer, nonce uint64) *types.Transaction {
	tx, err := types.GenerateTransactionWithSigner(nonce, to, big.NewInt(0), nil, s)
	if err != nil {
		panic(err)
	}
	return tx
}

// nolint
func constructTxs(s *types.Signer, count int) []*types.Transaction {
	txs := make([]*types.Transaction, count)
	for i := 0; i < count; i++ {
		tx, err := types.GenerateTransactionWithSigner(uint64(i), to, big.NewInt(0), nil, s)
		if err != nil {
			panic(err)
		}
		txs[i] = tx
	}
	return txs
}
