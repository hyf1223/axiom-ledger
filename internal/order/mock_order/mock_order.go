// Code generated by MockGen. DO NOT EDIT.
// Source: order.go
//
// Generated by this command:
//
//	mockgen -destination mock_order/mock_order.go -package mock_order -source order.go -typed
//
// Package mock_order is a generated GoMock package.
package mock_order

import (
	reflect "reflect"

	types "github.com/axiomesh/axiom-kit/types"
	common "github.com/axiomesh/axiom/internal/order/common"
	event "github.com/ethereum/go-ethereum/event"
	gomock "go.uber.org/mock/gomock"
)

// MockOrder is a mock of Order interface.
type MockOrder struct {
	ctrl     *gomock.Controller
	recorder *MockOrderMockRecorder
}

// MockOrderMockRecorder is the mock recorder for MockOrder.
type MockOrderMockRecorder struct {
	mock *MockOrder
}

// NewMockOrder creates a new mock instance.
func NewMockOrder(ctrl *gomock.Controller) *MockOrder {
	mock := &MockOrder{ctrl: ctrl}
	mock.recorder = &MockOrderMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockOrder) EXPECT() *MockOrderMockRecorder {
	return m.recorder
}

// Commit mocks base method.
func (m *MockOrder) Commit() chan *common.CommitEvent {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Commit")
	ret0, _ := ret[0].(chan *common.CommitEvent)
	return ret0
}

// Commit indicates an expected call of Commit.
func (mr *MockOrderMockRecorder) Commit() *OrderCommitCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Commit", reflect.TypeOf((*MockOrder)(nil).Commit))
	return &OrderCommitCall{Call: call}
}

// OrderCommitCall wrap *gomock.Call
type OrderCommitCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderCommitCall) Return(arg0 chan *common.CommitEvent) *OrderCommitCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderCommitCall) Do(f func() chan *common.CommitEvent) *OrderCommitCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderCommitCall) DoAndReturn(f func() chan *common.CommitEvent) *OrderCommitCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// GetPendingNonceByAccount mocks base method.
func (m *MockOrder) GetPendingNonceByAccount(account string) uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPendingNonceByAccount", account)
	ret0, _ := ret[0].(uint64)
	return ret0
}

// GetPendingNonceByAccount indicates an expected call of GetPendingNonceByAccount.
func (mr *MockOrderMockRecorder) GetPendingNonceByAccount(account any) *OrderGetPendingNonceByAccountCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPendingNonceByAccount", reflect.TypeOf((*MockOrder)(nil).GetPendingNonceByAccount), account)
	return &OrderGetPendingNonceByAccountCall{Call: call}
}

// OrderGetPendingNonceByAccountCall wrap *gomock.Call
type OrderGetPendingNonceByAccountCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderGetPendingNonceByAccountCall) Return(arg0 uint64) *OrderGetPendingNonceByAccountCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderGetPendingNonceByAccountCall) Do(f func(string) uint64) *OrderGetPendingNonceByAccountCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderGetPendingNonceByAccountCall) DoAndReturn(f func(string) uint64) *OrderGetPendingNonceByAccountCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// GetPendingTxByHash mocks base method.
func (m *MockOrder) GetPendingTxByHash(hash *types.Hash) *types.Transaction {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPendingTxByHash", hash)
	ret0, _ := ret[0].(*types.Transaction)
	return ret0
}

// GetPendingTxByHash indicates an expected call of GetPendingTxByHash.
func (mr *MockOrderMockRecorder) GetPendingTxByHash(hash any) *OrderGetPendingTxByHashCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPendingTxByHash", reflect.TypeOf((*MockOrder)(nil).GetPendingTxByHash), hash)
	return &OrderGetPendingTxByHashCall{Call: call}
}

// OrderGetPendingTxByHashCall wrap *gomock.Call
type OrderGetPendingTxByHashCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderGetPendingTxByHashCall) Return(arg0 *types.Transaction) *OrderGetPendingTxByHashCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderGetPendingTxByHashCall) Do(f func(*types.Hash) *types.Transaction) *OrderGetPendingTxByHashCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderGetPendingTxByHashCall) DoAndReturn(f func(*types.Hash) *types.Transaction) *OrderGetPendingTxByHashCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// Prepare mocks base method.
func (m *MockOrder) Prepare(tx *types.Transaction) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Prepare", tx)
	ret0, _ := ret[0].(error)
	return ret0
}

// Prepare indicates an expected call of Prepare.
func (mr *MockOrderMockRecorder) Prepare(tx any) *OrderPrepareCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Prepare", reflect.TypeOf((*MockOrder)(nil).Prepare), tx)
	return &OrderPrepareCall{Call: call}
}

// OrderPrepareCall wrap *gomock.Call
type OrderPrepareCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderPrepareCall) Return(arg0 error) *OrderPrepareCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderPrepareCall) Do(f func(*types.Transaction) error) *OrderPrepareCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderPrepareCall) DoAndReturn(f func(*types.Transaction) error) *OrderPrepareCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// Quorum mocks base method.
func (m *MockOrder) Quorum() uint64 {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Quorum")
	ret0, _ := ret[0].(uint64)
	return ret0
}

// Quorum indicates an expected call of Quorum.
func (mr *MockOrderMockRecorder) Quorum() *OrderQuorumCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Quorum", reflect.TypeOf((*MockOrder)(nil).Quorum))
	return &OrderQuorumCall{Call: call}
}

// OrderQuorumCall wrap *gomock.Call
type OrderQuorumCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderQuorumCall) Return(arg0 uint64) *OrderQuorumCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderQuorumCall) Do(f func() uint64) *OrderQuorumCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderQuorumCall) DoAndReturn(f func() uint64) *OrderQuorumCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// Ready mocks base method.
func (m *MockOrder) Ready() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Ready")
	ret0, _ := ret[0].(error)
	return ret0
}

// Ready indicates an expected call of Ready.
func (mr *MockOrderMockRecorder) Ready() *OrderReadyCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Ready", reflect.TypeOf((*MockOrder)(nil).Ready))
	return &OrderReadyCall{Call: call}
}

// OrderReadyCall wrap *gomock.Call
type OrderReadyCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderReadyCall) Return(arg0 error) *OrderReadyCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderReadyCall) Do(f func() error) *OrderReadyCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderReadyCall) DoAndReturn(f func() error) *OrderReadyCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// ReportState mocks base method.
func (m *MockOrder) ReportState(height uint64, blockHash *types.Hash, txHashList []*types.Hash) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "ReportState", height, blockHash, txHashList)
}

// ReportState indicates an expected call of ReportState.
func (mr *MockOrderMockRecorder) ReportState(height, blockHash, txHashList any) *OrderReportStateCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ReportState", reflect.TypeOf((*MockOrder)(nil).ReportState), height, blockHash, txHashList)
	return &OrderReportStateCall{Call: call}
}

// OrderReportStateCall wrap *gomock.Call
type OrderReportStateCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderReportStateCall) Return() *OrderReportStateCall {
	c.Call = c.Call.Return()
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderReportStateCall) Do(f func(uint64, *types.Hash, []*types.Hash)) *OrderReportStateCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderReportStateCall) DoAndReturn(f func(uint64, *types.Hash, []*types.Hash)) *OrderReportStateCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// Start mocks base method.
func (m *MockOrder) Start() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Start")
	ret0, _ := ret[0].(error)
	return ret0
}

// Start indicates an expected call of Start.
func (mr *MockOrderMockRecorder) Start() *OrderStartCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Start", reflect.TypeOf((*MockOrder)(nil).Start))
	return &OrderStartCall{Call: call}
}

// OrderStartCall wrap *gomock.Call
type OrderStartCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderStartCall) Return(arg0 error) *OrderStartCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderStartCall) Do(f func() error) *OrderStartCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderStartCall) DoAndReturn(f func() error) *OrderStartCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// Step mocks base method.
func (m *MockOrder) Step(msg []byte) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Step", msg)
	ret0, _ := ret[0].(error)
	return ret0
}

// Step indicates an expected call of Step.
func (mr *MockOrderMockRecorder) Step(msg any) *OrderStepCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Step", reflect.TypeOf((*MockOrder)(nil).Step), msg)
	return &OrderStepCall{Call: call}
}

// OrderStepCall wrap *gomock.Call
type OrderStepCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderStepCall) Return(arg0 error) *OrderStepCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderStepCall) Do(f func([]byte) error) *OrderStepCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderStepCall) DoAndReturn(f func([]byte) error) *OrderStepCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// Stop mocks base method.
func (m *MockOrder) Stop() {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "Stop")
}

// Stop indicates an expected call of Stop.
func (mr *MockOrderMockRecorder) Stop() *OrderStopCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Stop", reflect.TypeOf((*MockOrder)(nil).Stop))
	return &OrderStopCall{Call: call}
}

// OrderStopCall wrap *gomock.Call
type OrderStopCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderStopCall) Return() *OrderStopCall {
	c.Call = c.Call.Return()
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderStopCall) Do(f func()) *OrderStopCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderStopCall) DoAndReturn(f func()) *OrderStopCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// SubscribeTxEvent mocks base method.
func (m *MockOrder) SubscribeTxEvent(events chan<- []*types.Transaction) event.Subscription {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SubscribeTxEvent", events)
	ret0, _ := ret[0].(event.Subscription)
	return ret0
}

// SubscribeTxEvent indicates an expected call of SubscribeTxEvent.
func (mr *MockOrderMockRecorder) SubscribeTxEvent(events any) *OrderSubscribeTxEventCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SubscribeTxEvent", reflect.TypeOf((*MockOrder)(nil).SubscribeTxEvent), events)
	return &OrderSubscribeTxEventCall{Call: call}
}

// OrderSubscribeTxEventCall wrap *gomock.Call
type OrderSubscribeTxEventCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *OrderSubscribeTxEventCall) Return(arg0 event.Subscription) *OrderSubscribeTxEventCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *OrderSubscribeTxEventCall) Do(f func(chan<- []*types.Transaction) event.Subscription) *OrderSubscribeTxEventCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *OrderSubscribeTxEventCall) DoAndReturn(f func(chan<- []*types.Transaction) event.Subscription) *OrderSubscribeTxEventCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}
