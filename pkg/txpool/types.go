package txpool

import (
	"time"

	"github.com/axiomesh/axiom-bft/common/consensus"
)

// nolint
const (
	DefaultPoolSize            = 50000
	DefaultBatchSize           = 500
	DefaultToleranceNonceGap   = 1000
	DefaultToleranceTime       = 5 * time.Minute
	DefaultToleranceRemoveTime = 15 * time.Minute

	maxChanSize = 1024
)

type GetAccountNonceFunc func(address string) uint64

type TxInfo[T any, Constraint consensus.TXConstraint[T]] struct {
	Tx          *T
	Local       bool
	LifeTime    int64
	ArrivedTime int64
}

type AccountMeta[T any, Constraint consensus.TXConstraint[T]] struct {
	CommitNonce  uint64
	PendingNonce uint64
	TxCount      uint64
	Txs          []*TxInfo[T, Constraint]
	SimpleTxs    []*TxSimpleInfo
}

type TxSimpleInfo struct {
	Hash        string
	Nonce       uint64
	Size        int
	Local       bool
	LifeTime    int64
	ArrivedTime int64
}

type BatchSimpleInfo struct {
	TxCount   uint64
	Txs       []*TxSimpleInfo
	Timestamp int64
}

type Meta[T any, Constraint consensus.TXConstraint[T]] struct {
	TxCountLimit    uint64
	TxCount         uint64
	ReadyTxCount    uint64
	Batches         map[string]*BatchSimpleInfo
	MissingBatchTxs map[string]map[uint64]string
	Accounts        map[string]*AccountMeta[T, Constraint]
}

// RequestHashBatch contains transactions that batched by primary.
type RequestHashBatch[T any, Constraint consensus.TXConstraint[T]] struct {
	BatchHash  string   // hash of this batch calculated by MD5
	TxHashList []string // list of all txs' hashes
	TxList     []*T     // list of all txs
	LocalList  []bool   // list track if tx is received locally or not
	Timestamp  int64    // generation time of this batch
}

type txPointer struct {
	account string
	nonce   uint64
}

type internalTransaction[T any, Constraint consensus.TXConstraint[T]] struct {
	rawTx       *T
	local       bool
	lifeTime    int64 // track the local txs' broadcast time
	arrivedTime int64 // track the local txs' arrived txpool time
}

type txPoolEvent any

// =====================addTxsEvent=============================
const (
	localTxEvent = iota
	remoteTxsEvent
	missingTxsEvent
)

// LocalEvent represents event sent by local modules
type addTxsEvent struct {
	EventType int
	Event     any
}

type reqLocalTx[T any, Constraint consensus.TXConstraint[T]] struct {
	tx    *T
	errCh chan error
}

type reqRemoteTxs[T any, Constraint consensus.TXConstraint[T]] struct {
	txs []*T
}

type reqMissingTxs[T any, Constraint consensus.TXConstraint[T]] struct {
	batchHash string
	txs       map[uint64]*T
	errCh     chan error
}

// ================================================================

// =====================removeTxsEvent=============================
const (
	timeoutTxsEvent = iota
	highNonceTxsEvent
	committedTxsEvent
	batchedTxsEvent
)

type reqHighNonceTxs struct {
	account   string
	highNonce uint64
}

type removeTxsEvent struct {
	EventType int
	Event     any
}

type reqRemoveCommittedTxs struct {
	txHashList []string
}

type reqRemoveBatchedTxs struct {
	batchHashList []string
}

// ========================================================================

// ========================batchEvent===================================
const (
	GenBatchTimeoutEvent = iota
	GenBatchNoTxTimeoutEvent
	GenBatchFirstEvent
	GenBatchSizeEvent
	ReConstructBatchEvent
	GetTxsForGenBatchEvent
)

var batchEventToString = map[int]string{
	GenBatchTimeoutEvent:     "GenBatchTimeoutEvent",
	GenBatchNoTxTimeoutEvent: "GenBatchNoTxTimeoutEvent",
	GenBatchFirstEvent:       "GenBatchFirstEvent",
	GenBatchSizeEvent:        "GenBatchSizeEvent",
	ReConstructBatchEvent:    "ReConstructBatchEvent",
	GetTxsForGenBatchEvent:   "GetTxsForGenBatchEvent",
}

type batchEvent struct {
	EventType int
	Event     any
}

type reqGenBatch[T any, Constraint consensus.TXConstraint[T]] struct {
	respCh chan *respGenBatch[T, Constraint]
}

type respGenBatch[T any, Constraint consensus.TXConstraint[T]] struct {
	resp *RequestHashBatch[T, Constraint]
	err  error
}

type reqReConstructBatch[T any, Constraint consensus.TXConstraint[T]] struct {
	oldBatch *RequestHashBatch[T, Constraint]
	respCh   chan *respReConstructBatch
}

type respReConstructBatch struct {
	deDuplicateTxHashes []string
	err                 error
}

type reqGetTxsForGenBatch[T any, Constraint consensus.TXConstraint[T]] struct {
	batchHash           string
	timestamp           int64
	hashList            []string
	deDuplicateTxHashes []string

	respCh chan *respGetTxsForGenBatch[T, Constraint]
}

type respGetTxsForGenBatch[T any, Constraint consensus.TXConstraint[T]] struct {
	txs            []*T
	localList      []bool
	missingTxsHash map[uint64]string
	err            error
}

// ========================================================================

// =======================consensusEvent=============================
const (
	SendMissingTxsEvent = iota
	FilterReBroadcastTxsEvent
	RestoreOneBatchEvent
	RestoreAllBatchedEvent
)

type consensusEvent struct {
	EventType int
	Event     any
}

type reqSendMissingTxs[T any, Constraint consensus.TXConstraint[T]] struct {
	batchHash       string
	missingHashList map[uint64]string

	respCh chan *respSendMissingTxs[T, Constraint]
}

type respSendMissingTxs[T any, Constraint consensus.TXConstraint[T]] struct {
	resp map[uint64]*T
	err  error
}
type reqFilterReBroadcastTxs[T any, Constraint consensus.TXConstraint[T]] struct {
	respCh chan []*T
}

type reqRestoreOneBatch struct {
	batchHash string
	errCh     chan error
}

// =========================poolInfoEvent===============================
const (
	reqTxEvent = iota
	reqNonceEvent
	reqPendingTxCountEvent
	reqPoolMetaEvent
	reqAccountMetaEvent
	reqReadyGenBatchEvent
)

// poolInfoEvent represents poolInfo event sent by local api modules
type poolInfoEvent struct {
	EventType int
	Event     any
}

type reqTxMsg[T any, Constraint consensus.TXConstraint[T]] struct {
	hash string
	ch   chan *T
}

type reqNonceMsg struct {
	account string
	ch      chan uint64
}

type reqPendingTxCountMsg struct {
	ch chan uint64
}

type reqAccountPoolMetaMsg[T any, Constraint consensus.TXConstraint[T]] struct {
	account string
	full    bool
	ch      chan *AccountMeta[T, Constraint]
}

type reqPoolMetaMsg[T any, Constraint consensus.TXConstraint[T]] struct {
	full bool
	ch   chan *Meta[T, Constraint]
}
