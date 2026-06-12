package storage

import (
	"context"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// checkpointMetrics holds the OTel instruments for WAL checkpoint health. Like
// the consumer metrics, instruments come from the global meter and are valid
// no-ops before tracing.Init wires a MeterProvider, so construction never fails
// in tests or when telemetry is disabled.
type checkpointMetrics struct {
	total    metric.Int64Counter
	duration metric.Float64Histogram
	reg      metric.Registration
}

// newCheckpointMetrics builds the checkpoint instruments. walPath, when
// non-empty, is stat'd by an observable gauge so the live WAL sidecar size is
// visible in telemetry — the single number that tells us whether checkpoints are
// actually draining the WAL or it is growing unbounded under load.
func newCheckpointMetrics(walPath string) *checkpointMetrics {
	m := tracing.Meter()
	total, _ := m.Int64Counter(
		"bugbarn.wal.checkpoint.total",
		metric.WithDescription("WAL TRUNCATE checkpoint attempts by result (ok|busy|error)."),
		metric.WithUnit("{checkpoint}"),
	)
	duration, _ := m.Float64Histogram(
		"bugbarn.wal.checkpoint.duration",
		metric.WithDescription("Wall-clock time of a WAL checkpoint attempt, by result."),
		metric.WithUnit("ms"),
	)
	cm := &checkpointMetrics{total: total, duration: duration}

	if walPath != "" {
		gauge, err := m.Int64ObservableGauge(
			"bugbarn.wal.size_bytes",
			metric.WithDescription("Size of the SQLite WAL sidecar file (<db>-wal)."),
			metric.WithUnit("By"),
		)
		if err == nil {
			cm.reg, _ = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
				fi, statErr := os.Stat(walPath)
				if statErr != nil {
					return nil // WAL absent (no writes yet) or transient stat error.
				}
				o.ObserveInt64(gauge, fi.Size())
				return nil
			}, gauge)
		}
	}
	return cm
}

func (m *checkpointMetrics) record(ctx context.Context, result string, durMs float64) {
	if m == nil {
		return
	}
	m.total.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	m.duration.Record(ctx, durMs, metric.WithAttributes(attribute.String("result", result)))
}

func (m *checkpointMetrics) close() {
	if m != nil && m.reg != nil {
		_ = m.reg.Unregister()
	}
}

// RunPeriodicCheckpoint blocks until ctx is cancelled, issuing a WAL TRUNCATE
// checkpoint on each tick. It must run in the writer process only — the single
// owner of the write connection — so checkpoints serialise behind normal writes
// through MaxOpenConns(1) rather than contending on a second connection.
//
// With wal_autocheckpoint(0) set on the DSN, nothing else truncates the WAL:
// Litestream still streams frames to S3 but its own checkpoint has no
// busy_timeout and loses the race under sustained write load. This loop owns WAL
// truncation with a patient busy_timeout(30000) connection plus application-level
// retry for the reader-block case (TRUNCATE returns busy immediately when a
// reader snapshot pins the WAL; busy_timeout does not cover that phase).
func (s *Store) RunPeriodicCheckpoint(ctx context.Context, interval time.Duration, log *slog.Logger) {
	const retryInterval = 5 * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("wal checkpoint loop started", "interval", interval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Checkpoint(ctx, retryInterval, log)
		}
	}
}

// FinalCheckpoint runs one WAL TRUNCATE checkpoint on a fresh context. Call after
// all writers have stopped (after the worker WaitGroup drains) and before
// Close(), so the WAL is merged into the main file on every clean shutdown —
// with wal_autocheckpoint(0), Close() does not checkpoint automatically.
func (s *Store) FinalCheckpoint(log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.Checkpoint(ctx, 0, log)
}

// Checkpoint issues a single WAL TRUNCATE checkpoint, retrying every retryInterval
// while it is blocked by a reader snapshot (busy=1). retryInterval of 0 means try
// once and return — used by FinalCheckpoint on shutdown. Each attempt records a
// result-tagged counter + duration; the WAL-size gauge reports the effect.
func (s *Store) Checkpoint(ctx context.Context, retryInterval time.Duration, log *slog.Logger) {
	for {
		start := time.Now()
		var busy, walFrames, checkpointed int
		err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &walFrames, &checkpointed)
		durMs := float64(time.Since(start).Microseconds()) / 1000.0
		switch {
		case err != nil:
			if ctx.Err() == nil {
				log.Warn("wal checkpoint error", "error", err)
			}
			s.checkpoints.record(ctx, "error", durMs)
			return
		case busy == 0:
			s.checkpoints.record(ctx, "ok", durMs)
			return
		default:
			// busy=1: a reader snapshot blocks full WAL backfill. Retry after a
			// short interval so the WAL can be drained once the reader finishes.
			s.checkpoints.record(ctx, "busy", durMs)
			log.Debug("wal checkpoint blocked by reader, retrying", "wal_frames", walFrames, "checkpointed", checkpointed)
			if retryInterval == 0 {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryInterval):
			}
		}
	}
}
