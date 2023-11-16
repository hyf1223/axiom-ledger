package txpool

func (p *txPoolImpl[T, Constraint]) setFull() {
	p.statusMgr.On(PoolFull)
}

func (p *txPoolImpl[T, Constraint]) setNotFull() {
	p.statusMgr.Off(PoolFull)
}

func (p *txPoolImpl[T, Constraint]) setReady() {
	p.statusMgr.On(ReadyGenerateBatch)
}

func (p *txPoolImpl[T, Constraint]) setNotReady() {
	p.statusMgr.Off(ReadyGenerateBatch)
}

func (p *txPoolImpl[T, Constraint]) setHasPendingRequest() {
	p.statusMgr.On(HasPendingRequest)
}

func (p *txPoolImpl[T, Constraint]) setNoPendingRequest() {
	p.statusMgr.Off(HasPendingRequest)
}
