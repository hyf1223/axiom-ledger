package txpool

import "github.com/axiomesh/axiom-ledger/internal/components/timer"

func (p *txPoolImpl[T, Constraint]) handleRemoveTimeout(_ timer.TimeoutEvent) {
	p.timerMgr.StopTimer(timer.RemoveTx)
	ev := &removeTxsEvent{
		EventType: timeoutTxsEvent,
	}
	p.postEvent(ev)
	err := p.timerMgr.RestartTimer(timer.RemoveTx)
	if err != nil {
		p.logger.Warning("failed to restart timer", err)
	}
}
