package txpool

import (
	"github.com/axiomesh/axiom-bft/common/consensus"
)

//go:generate mockgen -destination mock_txpool/mock_txpool.go -package mock_txpool -source txpool.go -typed
type TxPool[T any, Constraint consensus.TXConstraint[T]] interface {
	// Query txPool info from api
	txPoolInfo[T, Constraint]

	Init(config ConsensusConfig)

	// Start starts txPool service
	Start() error
	// Stop stops txPool service
	Stop()

	// AddLocalTx add local tx into txPool
	AddLocalTx(tx *T) error

	// AddRemoteTxs add p2p txs into txPool
	AddRemoteTxs(txs []*T)

	// GenerateRequestBatch generates a transaction batch and post it
	// to outside if there are transactions in txPool.
	GenerateRequestBatch(typ int) (*RequestHashBatch[T, Constraint], error)

	// RemoveBatches removes several batches by given digests of
	// transaction batches from the pool(batchedTxs).
	RemoveBatches(batchHashList []string)

	// RemoveStateUpdatingTxs removes all committed txs when state update one block during state updating
	RemoveStateUpdatingTxs(txHashList []string)

	// RestorePool move all batched txs back to non-batched tx which should
	// only be used after abnormal recovery.
	RestorePool()

	// ReConstructBatchByOrder reconstruct batch from empty txPool by order, must be called after RestorePool.
	ReConstructBatchByOrder(oldBatch *RequestHashBatch[T, Constraint]) (deDuplicateTxHashes []string, err error)

	FilterOutOfDateRequests() []*T

	// RestoreOneBatch moves one batch from batchStore back to non-batched txs.
	RestoreOneBatch(hash string) error

	// GetRequestsByHashList returns the transaction list corresponding to the given hash list.
	// When replicas receive hashList from primary, they need to generate a totally same
	// batch to primary generated one. deDuplicateTxHashes specifies some txs which should
	// be excluded from duplicate rules.
	// 1. If this batch has been batched, just return its transactions without error.
	// 2. If we have checked this batch and found we were missing some transactions, just
	//    return the same missingTxsHash as before without error.
	// 3. If one transaction in hashList has been batched before in another batch,
	//    return ErrDuplicateTx
	// 4. If we miss some transactions, we need to fetch these transactions from primary,
	//    and return missingTxsHash without error
	// 5. If this node get all transactions from pool, generate a batch and return its
	//    transactions without error
	GetRequestsByHashList(batchHash string, timestamp int64, hashList []string, deDuplicateTxHashes []string) (txs []*T, list []bool, missingTxsHash map[uint64]string, err error)

	// SendMissingRequests used by primary to find one batch in batchStore which should contain
	// txs which are specified in missingHashList.
	// 1. If there is no such batch, return ErrNoBatch.
	// 2. If there is such a batch, but it doesn't contain all txs in missingHashList,
	//    return ErrMismatch.
	// 3. If there is such a batch, and contains all needed txs, returns all needed txs by
	//    order.
	SendMissingRequests(batchHash string, missingHashList map[uint64]string) (txs map[uint64]*T, err error)

	// ReceiveMissingRequests receives txs fetched from primary and add txs to txPool
	ReceiveMissingRequests(batchHash string, txs map[uint64]*T) error
}

type txPoolInfo[T any, Constraint consensus.TXConstraint[T]] interface {
	// GetPendingTxCountByAccount will return the pending tx count by account in txpool
	GetPendingTxCountByAccount(account string) uint64

	// GetPendingTxByHash will return the pending tx by hash in txpool
	GetPendingTxByHash(txHash string) *T

	// GetTotalPendingTxCount will return the number of pending txs in txpool
	GetTotalPendingTxCount() uint64

	// GetAccountMeta will return the account meta by account, if full is true, will return the full tx by account
	GetAccountMeta(account string, full bool) *AccountMeta[T, Constraint]

	// GetMeta will return the all accounts' meta, if full is true, will return the full tx
	GetMeta(full bool) *Meta[T, Constraint]

	// IsPoolFull check if txPool is full which means if number of all cached txs
	// has exceeded the limited poolSize.
	IsPoolFull() bool

	PendingRequestsNumberIsReady() bool

	HasPendingRequestInPool() bool
}

func NewTxPool[T any, Constraint consensus.TXConstraint[T]](config Config) (TxPool[T, Constraint], error) {
	return newTxPoolImpl[T, Constraint](config)
}
