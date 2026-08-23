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

	// batchPause is the gap between batches, during which ingest has the writer
	// to itself.
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
	batchPause = time.Second

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
// and budget logic without sleeping through the real batchPause, which would
// make a full-budget sweep take the better part of a minute.
type tuning struct {
	batchSize   int
	pause       time.Duration
	maxPerSweep int64
}

func defaultTuning() tuning {
	return tuning{batchSize: batchSize, pause: batchPause, maxPerSweep: maxDeletesPerSweep}
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

// sweep deletes expired events in batches until the backlog is drained, the
// per-sweep budget is spent, or the process is shutting down.
func sweep(ctx context.Context, store Store, cfg Config, tun tuning, log *slog.Logger, m *workerMetrics) {
	cutoff := time.Now().UTC().AddDate(0, 0, -cfg.RetentionDays)
	start := time.Now()

	var deleted int64
	for deleted < tun.maxPerSweep {
		if ctx.Err() != nil {
			return
		}
		n, err := store.DeleteEventsBefore(ctx, cutoff, tun.batchSize)
		if err != nil {
			if !shuttingDown(err) {
				log.Error("retention: delete batch failed",
					"cutoff", cutoff.Format(time.RFC3339), "deleted_so_far", deleted, "error", err)
			}
			return
		}
		deleted += n
		if m != nil {
			m.eventsDeleted.Add(ctx, n)
		}
		// A short batch means nothing older than the cutoff is left.
		if n < int64(tun.batchSize) {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tun.pause):
		}
	}

	if deleted == 0 {
		log.Debug("retention: nothing to expire", "cutoff", cutoff.Format(time.RFC3339))
		return
	}

	// Report the remaining backlog so a sweep that hit its budget is visible as
	// "still draining" rather than looking like a completed run. The COUNT is a
	// scan, so it runs once per sweep and only when the sweep did work.
	remaining, err := store.CountEventsBefore(ctx, cutoff)
	if err != nil && !shuttingDown(err) {
		log.Warn("retention: could not measure remaining backlog", "error", err)
	}
	log.Info("retention: expired events",
		"deleted", deleted,
		"remaining", remaining,
		"cutoff", cutoff.Format(time.RFC3339),
		"retention_days", cfg.RetentionDays,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}
