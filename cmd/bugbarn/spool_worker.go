package main

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

// spoolPersister adapts the ingestproc pipeline to the worker's RecordPersister.
// internal/worker cannot import ingestproc (ingestproc imports storage, which
// imports worker), so the outcome is translated here.
type spoolPersister struct {
	proc *ingestproc.Processor
}

func (p spoolPersister) PersistRecord(ctx context.Context, record spool.Record) worker.PersistResult {
	res := p.proc.PersistRecord(ctx, record)
	return worker.PersistResult{
		Outcome:     workerOutcome(res.Outcome),
		Err:         res.Err,
		IsNew:       res.IsNew,
		Regressed:   res.Regressed,
		Fingerprint: res.Fingerprint,
		Severity:    res.Severity,
	}
}

func (p spoolPersister) PersistRelease(ctx context.Context, record spool.Record) error {
	return p.proc.PersistRelease(ctx, record)
}

func workerOutcome(o ingestproc.Outcome) worker.PersistOutcome {
	switch o {
	case ingestproc.OutcomeSuccess:
		return worker.OutcomeSuccess
	case ingestproc.OutcomeHeld:
		return worker.OutcomeHeld
	case ingestproc.OutcomeTransient:
		return worker.OutcomeTransient
	case ingestproc.OutcomeParseError:
		return worker.OutcomeParseError
	default:
		return worker.OutcomePersistError
	}
}

// runBackgroundWorker builds the spool worker over the writer's store and
// pipeline and runs it until ctx ends.
func runBackgroundWorker(ctx context.Context, eventSpool *spool.Spool, spoolDir string, store *storage.Store, proc *ingestproc.Processor, selfReporting bool, ws *worker.Status, mq *mutqueue.Queue) {
	worker.RunSpoolWorker(ctx, worker.SpoolWorkerConfig{
		EventSpool:    eventSpool,
		SpoolDir:      spoolDir,
		Store:         store,
		Persister:     spoolPersister{proc: proc},
		SelfReporting: selfReporting,
		Status:        ws,
		Mutations:     mq,
	})
}
