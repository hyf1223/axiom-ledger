package app

import (
	"github.com/sirupsen/logrus"

	"github.com/axiomesh/axiom-ledger/pkg/events"
)

func (axm *AxiomLedger) start() {
	go axm.listenWaitReportBlock()
	go axm.listenWaitExecuteBlock()
}

func (axm *AxiomLedger) listenWaitReportBlock() {
	if axm.Repo.ReadonlyMode {
		return
	}

	blockCh := make(chan events.ExecutedEvent)
	blockSub := axm.BlockExecutor.SubscribeBlockEvent(blockCh)
	defer blockSub.Unsubscribe()

	for {
		select {
		case <-axm.Ctx.Done():
			return
		case ev := <-blockCh:
			axm.Consensus.ReportState(ev.Block.BlockHeader.Number, ev.Block.BlockHash, ev.TxHashList, ev.StateUpdatedCheckpoint)
		}
	}
}

func (axm *AxiomLedger) listenWaitExecuteBlock() {
	if axm.Repo.ReadonlyMode {
		return
	}
	for {
		select {
		case commitEvent := <-axm.Consensus.Commit():
			axm.logger.WithFields(logrus.Fields{
				"height": commitEvent.Block.BlockHeader.Number,
				"count":  len(commitEvent.Block.Transactions),
			}).Info("Generated block")
			axm.BlockExecutor.AsyncExecuteBlock(commitEvent)
		case <-axm.Ctx.Done():
			return
		}
	}
}
