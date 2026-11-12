package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/monitorwatch"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

var errMonitorEventRejected = errors.New("monitor event was not persisted")

// internalPersist persists an event BugBarn files itself (a monitor incident, a
// silent project) through the same pipeline as SDK events. A nil error means the
// event is in its project or held for an unapproved one.
func internalPersist(proc *ingestproc.Processor) func(ctx context.Context, rec spool.Record) error {
	return func(ctx context.Context, rec spool.Record) error {
		res := proc.PersistRecord(ctx, rec)
		switch res.Outcome {
		case ingestproc.OutcomeSuccess, ingestproc.OutcomeHeld:
			return nil
		}
		if res.Err != nil {
			return res.Err
		}
		return errMonitorEventRejected
	}
}

// startMonitors starts the check-in miss detector and returns the reporter the
// API uses for error and recovery check-ins. Writer-only: both persist through
// the same pipeline as SDK events, so each incident becomes an issue in the
// monitor's own project.
func startMonitors(ctx context.Context, store *storage.Store, proc *ingestproc.Processor, wg *sync.WaitGroup, log *slog.Logger) *monitorwatch.Reporter {
	reporter := monitorwatch.NewReporter(store, internalPersist(proc), log)
	monitorwatch.NewDetector(monitorwatch.DefaultConfig(), store, reporter, log).Start(ctx, wg)
	return reporter
}
