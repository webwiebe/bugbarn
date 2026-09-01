// Package retention expires event rows past their retention window.
//
// Events were the only unbounded table in the database. Logs have a soft
// per-project cap, analytics page-views a 90-day window and web sessions an
// expiry sweep, but every event ever ingested was kept forever. In production
// that meant ~3.6M rows of which ~3.3M belonged to a single integration and
// ~1.8M to one already-resolved issue, which is both most of the database file
// and the reason the per-project usage aggregate had to scan for over a second.
//
// The worker trims in small batches with a pause between them, because the
// alternative — one big DELETE — would hold the single shared write connection
// for the whole sweep and stall ingest behind it.
package retention

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// Store is the subset of storage.Store the retention worker needs.
type Store interface {
	DeleteEventsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
	CountEventsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

const (
	// DefaultRetentionDays is the default event retention window.
	DefaultRetentionDays = 30

	// sweepInterval is how often the worker looks for expired events. Retention
	// is not time-critical — an event that lives an extra hour past its window
	// costs nothing — so this is deliberately slow relative to ingest.
	sweepInterval = time.Hour

	// batchSize is how many events one DELETE statement removes. Small enough
	// that the statement (plus its cascaded event_facets deletes) occupies the
	// single writer for tens of milliseconds, not seconds.
	batchSize = 2000

	// minBatchPause is the floor on the gap between batches, during which
	// ingest has the writer to itself.
	//
	// It is sized by WAL growth, not by CPU. Nothing checkpoints the WAL except
	// the writer's own TRUNCATE loop on a 60s tick, so what matters is how much
	// a sweep can write between two checkpoints. Deleting an event row also
	// writes its (potentially kilobyte-scale) event_json into the WAL, plus its
	// cascaded facets. At a 200ms pause a sweep would push on the order of half
	// a million rows through the WAL inside one checkpoint window — past the
	// ~377MB that wedged production on 2026-07-16. A one-second pause holds it
	// to roughly 60 batches per window instead, which the next TRUNCATE clears
	// comfortably.
	//
	// Do not "fix" this by checkpointing from the sweep: the TRUNCATE loop is
	// deliberately the sole checkpointer, and a second one just races it for
	// the write lock.
	minBatchPause = time.Second

	// maxBatchPause caps the proportional pause (see pauseFor) so a pathological
	// batch cannot stall retention outright. The sweep budget already bounds the
	// whole run, so this only shapes the duty cycle within it.
	maxBatchPause = 30 * time.Second

	// sweepBudget bounds one sweep's wall clock, the same way
	// checkpointTickBudget bounds one checkpoint tick (#166). maxDeletesPerSweep
	// alone does not: it counts rows, and rows only bound time if you already
	// know what a batch costs. Production showed a 2000-row batch costing tens
	// of seconds rather than the tens of milliseconds this package assumed, at
	// which point a full 500k-row budget is hours of sweeping, overrunning the
	// hourly interval. Bounding the clock instead means a slow database simply
	// drains over more ticks.
	sweepBudget = 20 * time.Minute

	// maxDeletesPerSweep bounds one sweep so a huge first-run backlog is spread
	// over several hours instead of running for an unbounded stretch.
	//
	// Measured on staging (9GB database): a full-budget sweep deletes 500k
	// events in about nine minutes — comfortably inside the hourly interval, so
	// sweeps never overlap — while ingest stayed healthy throughout (queue
	// depth 0) and the WAL held around 85MB, well under the 256MB warning
	// threshold. A multi-million-row backlog therefore drains over a handful of
	// hourly sweeps. Steady state is far smaller: one sweep expires about a
	// day of events and finishes in seconds.
	maxDeletesPerSweep = 500_000
)

// Config controls the retention sweep.
type Config struct {
	// RetentionDays is the age past which events are deleted. Values <= 0 fall
	// back to DefaultRetentionDays; retention cannot be disabled by
	// misconfiguration, only widened.
	RetentionDays int
}

// tuning is the sweep's pacing. It exists so tests can exercise the batching
// and budget logic without sleeping through the real pauses, which would make a
// full-budget sweep take the better part of a minute.
type tuning struct {
	batchSize   int
	minPause    time.Duration
	maxPause    time.Duration
	budget      time.Duration
	maxPerSweep int64
}

func defaultTuning() tuning {
	return tuning{
		batchSize:   batchSize,
		minPause:    minBatchPause,
		maxPause:    maxBatchPause,
		budget:      sweepBudget,
		maxPerSweep: maxDeletesPerSweep,
	}
}

// pauseFor sizes the gap after a batch that held the writer for d.
//
// The pause is proportional to what the batch just cost, so the sweep hands
// back at least as much of the single write connection as it consumed. The old
// fixed one-second pause encoded an assumption in batchSize's comment — that a
// batch occupies the writer "for tens of milliseconds, not seconds" — which
// production disproved: batches ran tens of seconds, so a 1s gap left ingest
// roughly 3% of the writer instead of nearly all of it. Log inserts arriving
// during a sweep then exhausted every retry layer and were dropped outright
// (BS2-98), and the drops tracked sweep length exactly: none at 177-180s,
// every sweep from 214s up.
//
// Proportional pacing fixes that without needing to know which part of a batch
// is slow, because it measures the batch instead of predicting it. On a fast
// database d is tiny, the floor applies, and behavior is unchanged.
func pauseFor(d time.Duration, tun tuning) time.Duration {
	p := d
	if p < tun.minPause {
		p = tun.minPause
	}
	if tun.maxPause > 0 && p > tun.maxPause {
		p = tun.maxPause
	}
	return p
}

type workerMetrics struct {
	eventsDeleted metric.Int64Counter
}

func newWorkerMetrics() *workerMetrics {
	deleted, _ := tracing.Meter().Int64Counter(
		"bugbarn.retention.events_deleted",
		metric.WithDescription("Event rows deleted by the retention sweep."),
		metric.WithUnit("{event}"),
	)
	return &workerMetrics{eventsDeleted: deleted}
}

// StartWorker runs the retention sweep on an hourly cadence until ctx is
// canceled, beginning with one sweep on startup. If wg is non-nil it is
// incremented before the goroutine starts and decremented when it exits.
//
// Call this on the writer only. On a reader the store has no write connection
// and every batch would fail against a read-only database.
func StartWorker(ctx context.Context, store Store, cfg Config, log *slog.Logger, wg *sync.WaitGroup) {
	if store == nil {
		return
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = DefaultRetentionDays
	}
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "retention")
	m := newWorkerMetrics()

	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		defer func() {
			if p := recover(); p != nil {
				log.Error("retention worker panic", "panic", p)
			}
		}()

		tun := defaultTuning()
		log.Info("event retention enabled", "retention_days", cfg.RetentionDays)
		sweep(ctx, store, cfg, tun, log, m)

		t := time.NewTicker(sweepInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sweep(ctx, store, cfg, tun, log, m)
			}
		}
	}()
}

// shuttingDown reports whether err is just the process stopping. A sweep
// interrupted by shutdown is expected, not a fault — logging it at ERROR would
// file a bug against ourselves on every deploy, because selflog reports every
// error-level record to our own ingest endpoint.
func shuttingDown(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// sweepResult is what one pass of the batch loop accomplished.
type sweepResult struct {
	deleted int64
	// drained reports that a short batch proved nothing older than the cutoff
	// is left, which makes the remaining backlog 0 without having to count it.
	drained bool
	// stop names why the loop ended, for the summary log line.
	stop string
	// aborted means shutdown or a delete error ended the sweep; the caller logs
	// nothing further.
	aborted    bool
	deleteTime time.Duration
	pauseTime  time.Duration
}

// drainBatches deletes expired events in batches until the backlog is drained,
// a budget is spent, or the process is shutting down.
func drainBatches(
	ctx context.Context, store Store, cutoff time.Time, tun tuning, log *slog.Logger, m *workerMetrics,
) sweepResult {
	res := sweepResult{stop: "row_budget"}
	deadline := time.Now().Add(tun.budget)

	for res.deleted < tun.maxPerSweep {
		if ctx.Err() != nil {
			res.aborted = true
			return res
		}
		batchStart := time.Now()
		n, err := store.DeleteEventsBefore(ctx, cutoff, tun.batchSize)
		batchDur := time.Since(batchStart)
		res.deleteTime += batchDur
		if err != nil {
			if !shuttingDown(err) {
				log.Error("retention: delete batch failed",
					"cutoff", cutoff.Format(time.RFC3339), "deleted_so_far", res.deleted, "error", err)
			}
			res.aborted = true
			return res
		}
		res.deleted += n
		if m != nil {
			m.eventsDeleted.Add(ctx, n)
		}
		// A short batch means nothing older than the cutoff is left.
		if n < int64(tun.batchSize) {
			res.drained, res.stop = true, "drained"
			return res
		}
		if tun.budget > 0 && !time.Now().Before(deadline) {
			res.stop = "time_budget"
			return res
		}
		pause := pauseFor(batchDur, tun)
		select {
		case <-ctx.Done():
			res.aborted = true
			return res
		case <-time.After(pause):
		}
		res.pauseTime += pause
	}
	return res
}

// sweep runs one retention pass and reports what it did.
func sweep(ctx context.Context, store Store, cfg Config, tun tuning, log *slog.Logger, m *workerMetrics) {
	cutoff := time.Now().UTC().AddDate(0, 0, -cfg.RetentionDays)
	start := time.Now()

	res := drainBatches(ctx, store, cutoff, tun, log, m)
	if res.aborted {
		return
	}
	if res.deleted == 0 {
		log.Debug("retention: nothing to expire", "cutoff", cutoff.Format(time.RFC3339))
		return
	}

	// Only count when the sweep stopped early. A drained sweep already proved
	// the backlog is empty, and CountEventsBefore is a full scan of events —
	// every index on the table is prefixed by project_id, so a cross-project
	// `received_at < ?` can use none of them. Counting unconditionally meant
	// scanning the whole table on every steady-state sweep purely to log
	// "remaining": 0, which is exactly what production logged every hour.
	var remaining int64
	var countMS int64
	if !res.drained {
		countStart := time.Now()
		n, err := store.CountEventsBefore(ctx, cutoff)
		countMS = time.Since(countStart).Milliseconds()
		if err != nil && !shuttingDown(err) {
			log.Warn("retention: could not measure remaining backlog", "error", err)
		}
		remaining = n
	}

	// delete_ms vs pause_ms is the sweep's duty cycle on the single writer — the
	// number to look at first if ingest starves during retention again.
	log.Info("retention: expired events",
		"deleted", res.deleted,
		"remaining", remaining,
		"stop", res.stop,
		"cutoff", cutoff.Format(time.RFC3339),
		"retention_days", cfg.RetentionDays,
		"duration_ms", time.Since(start).Milliseconds(),
		"delete_ms", res.deleteTime.Milliseconds(),
		"pause_ms", res.pauseTime.Milliseconds(),
		"count_ms", countMS,
	)
}
