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

// startMonitors starts the check-in miss detector and returns the reporter the
// API uses for error and recovery check-ins. Writer-only: both persist through
// the same pipeline as SDK events, so each incident becomes an issue in the
// monitor's own project.
func startMonitors(ctx context.Context, store *storage.Store, proc *ingestproc.Processor, wg *sync.WaitGroup, log *slog.Logger) *monitorwatch.Reporter {
	persist := func(ctx context.Context, rec spool.Record) error {
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
	reporter := monitorwatch.NewReporter(store, persist, log)
	monitorwatch.NewDetector(monitorwatch.DefaultConfig(), store, reporter, log).Start(ctx, wg)
	return reporter
}
