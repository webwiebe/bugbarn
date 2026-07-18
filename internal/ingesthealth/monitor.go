// Package ingesthealth watches the ingest write pipeline for liveness and makes
// a stall loud instead of silent. The 2026-06-21 outage went unnoticed for ~5
// days because reads kept serving a stale database while no events were being
// persisted: the dashboard looked healthy. This monitor samples three signals —
// how long since the last event was persisted, how deep the Redis write queue
// is, and how large the SQLite WAL has grown — and surfaces them three ways:
//
//   - a Snapshot the detailed health endpoint folds in, so the external Health
//     Check probe gets a 503 when ingest stalls (a channel that does not depend
//     on the wedged write path);
//   - OTel gauges for dashboards/alerting;
//   - out-of-band Notifiers (webhook, SMTP) that carry the alert off the box
//     without touching the write queue or the store;
//   - throttled ERROR logs, which selflog reports to BugBarn itself (best effort:
//     during a total wedge the self-report queues too, so the health probe and
//     the notifiers are the authoritative signals).
//
// The self-report path is circular by construction — it announces that ingest is
// broken by way of ingest, and a 2026-07-16 backlog hid a stall for ~13h because
// the alert sat behind 103k queued items. Everything above except the ERROR log
// is deliberately independent of the pipeline it watches.
package ingesthealth

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// Config holds the monitor's sampling cadence and the thresholds that flip the
// pipeline to unhealthy. Zero values fall back to the defaults in New.
type Config struct {
	// Interval is how often the monitor samples. Default 60s.
	Interval time.Duration
	// StaleAfter is the maximum age of the most recent persisted event before a
	// backed-up queue is considered a stall. Default 30m; wired to
	// BUGBARN_INGEST_STALE_AFTER_SECONDS so it can be raised on an environment
	// whose quiet periods run long (e.g. an idle testing box that has no write
	// queue to distinguish idle from stalled — see evaluate). Staleness alone
	// never flips a queue-backed instance unhealthy; the queue depth must
	// corroborate it.
	StaleAfter time.Duration
	// MaxQueueDepth is the write-queue backlog (entries) above which ingest is
	// considered backed up. Default 50_000. Zero disables the check.
	MaxQueueDepth int64
	// WarnWALBytes is the WAL size above which a warning is logged (the WAL not
	// truncating is the leading indicator of checkpoint contention). It does not
	// by itself mark the pipeline unhealthy. Default 256 MiB. Zero disables.
	WarnWALBytes int64
	// AlertEvery throttles repeated unhealthy logs for the same condition.
	// Default 15m.
	AlertEvery time.Duration
	// NotifyTimeout bounds a single out-of-band delivery round. Default 30s.
	NotifyTimeout time.Duration
	// Environment (production/staging/testing) labels outgoing alerts so a
	// recipient can tell which instance is stalled. Optional.
	Environment string
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	if c.StaleAfter <= 0 {
		c.StaleAfter = 30 * time.Minute
	}
	if c.MaxQueueDepth == 0 {
		c.MaxQueueDepth = 50_000
	}
	if c.WarnWALBytes == 0 {
		c.WarnWALBytes = 256 << 20
	}
	if c.AlertEvery <= 0 {
		c.AlertEvery = 15 * time.Minute
	}
	if c.NotifyTimeout <= 0 {
		c.NotifyTimeout = 30 * time.Second
	}
	return c
}

// Snapshot is the most recent sample. It is safe to read concurrently.
type Snapshot struct {
	// Sampled is false until the first sample completes.
	Sampled             bool      `json:"sampled"`
	Healthy             bool      `json:"healthy"`
	Reasons             []string  `json:"reasons,omitempty"`
	SampledAt           time.Time `json:"sampledAt"`
	LastEventAt         time.Time `json:"lastEventAt"`
	LastEventAgeSeconds float64   `json:"lastEventAgeSeconds"`
	HasEvents           bool      `json:"hasEvents"`
	QueueDepth          int64     `json:"queueDepth"`
	QueueDepthKnown     bool      `json:"queueDepthKnown"`
	WALSizeBytes        int64     `json:"walSizeBytes"`
}

// Deps are the data sources the monitor samples. QueueDepth may be nil (e.g. a
// writer not fronted by a Redis queue), in which case the backlog check is
// skipped. dbPath, when non-empty, locates the SQLite file whose "-wal" sibling
// is measured.
type Deps struct {
	LastEventAt func(ctx context.Context) (time.Time, error)
	QueueDepth  func(ctx context.Context) (int64, error)
	DBPath      string
}

// Monitor samples ingest liveness on a cadence and publishes a Snapshot.
type Monitor struct {
	cfg    Config
	deps   Deps
	logger *slog.Logger
	now    func() time.Time

	snap atomic.Pointer[Snapshot]

	mu        sync.Mutex
	lastAlert time.Time

	notifiers []Notifier

	reg metric.Registration
}

// AddNotifier registers an out-of-band alert channel. Nil notifiers are ignored
// so callers can pass an unconfigured channel straight through. Call before
// Start; notifiers are read by the sample loop.
func (m *Monitor) AddNotifier(notifiers ...Notifier) {
	for _, n := range notifiers {
		if isNilNotifier(n) {
			continue
		}
		m.notifiers = append(m.notifiers, n)
	}
}

// isNilNotifier reports whether n carries no value. The constructors return a
// typed nil pointer when a channel is unconfigured, and a nil pointer in an
// interface is not == nil — so a plain nil check is not enough.
func isNilNotifier(n Notifier) bool {
	if n == nil {
		return true
	}
	v := reflect.ValueOf(n)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

// New builds a monitor. logger may be nil. now may be nil (defaults to
// time.Now); it exists for tests.
func New(cfg Config, deps Deps, logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Monitor{
		cfg:    cfg.withDefaults(),
		deps:   deps,
		logger: logger.With("component", "ingest-health"),
		now:    time.Now,
	}
	empty := &Snapshot{}
	m.snap.Store(empty)
	return m
}

// Snapshot returns the most recent sample.
func (m *Monitor) Snapshot() Snapshot {
	return *m.snap.Load()
}

// Start registers gauges and runs the sample loop until ctx is cancelled. It
// performs one sample immediately so the snapshot is populated before the first
// tick. Safe to run as a goroutine.
func (m *Monitor) Start(ctx context.Context) {
	m.registerGauges()
	defer m.unregisterGauges()

	m.sample(ctx)
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sample(ctx)
		}
	}
}

// sample collects the raw signals, evaluates health, stores the snapshot, and
// emits a throttled log when unhealthy.
func (m *Monitor) sample(ctx context.Context) {
	now := m.now().UTC()
	snap := Snapshot{Sampled: true, SampledAt: now, Healthy: true}

	if m.deps.LastEventAt != nil {
		last, err := m.deps.LastEventAt(ctx)
		if err != nil {
			m.logger.Error("ingest-health: query last event time", "error", err)
		} else if last.IsZero() {
			// No events ever persisted — treat as "no data" rather than stalled,
			// so a fresh deployment is not flagged.
			snap.HasEvents = false
		} else {
			snap.HasEvents = true
			snap.LastEventAt = last.UTC()
			snap.LastEventAgeSeconds = now.Sub(last).Seconds()
		}
	}

	if m.deps.QueueDepth != nil {
		depth, err := m.deps.QueueDepth(ctx)
		if err != nil {
			m.logger.Error("ingest-health: query queue depth", "error", err)
		} else {
			snap.QueueDepthKnown = true
			snap.QueueDepth = depth
		}
	}

	if m.deps.DBPath != "" {
		if info, err := os.Stat(m.deps.DBPath + "-wal"); err == nil {
			snap.WALSizeBytes = info.Size()
		}
	}

	m.evaluate(&snap, now)

	m.snap.Store(&snap)
	m.maybeAlert(ctx, snap)
}

// evaluate applies the health rules to a freshly gathered snapshot. It runs
// after every signal is collected because the staleness verdict depends on the
// write-queue depth.
//
// Staleness on its own is ambiguous: "no event persisted recently" is a stall
// only when events are actually arriving and failing to drain. On an idle
// instance — staging and testing sit quiet for hours or days between deploys —
// no traffic is the normal state, not an outage. Treating that as unhealthy
// produced a flood of false "ingest pipeline unhealthy" alerts (155/day across
// the two non-prod boxes) while every one had queue_depth 0 and an empty WAL.
//
// So a stale last-event is only a stall when the write queue corroborates it
// (depth > 0: events are queued but not being persisted), or when the queue
// depth is unknown — a monolith with no queue to consult, where staleness is
// the only signal available and the original silent-outage protection must be
// preserved. A known-empty queue behind a stale last-event is idle, not stalled.
func (m *Monitor) evaluate(snap *Snapshot, now time.Time) {
	if m.cfg.StaleAfter > 0 && snap.HasEvents {
		if age := now.Sub(snap.LastEventAt); age > m.cfg.StaleAfter {
			switch {
			case snap.QueueDepthKnown && snap.QueueDepth == 0:
				// Idle: nothing is waiting to be persisted. Not a stall.
			case snap.QueueDepthKnown:
				snap.Healthy = false
				snap.Reasons = append(snap.Reasons, fmt.Sprintf(
					"events queued but not persisted for %s (queue depth %d, threshold %s)",
					age.Round(time.Second), snap.QueueDepth, m.cfg.StaleAfter))
			default:
				// No queue visibility (monolith): fall back to staleness alone.
				snap.Healthy = false
				snap.Reasons = append(snap.Reasons, fmt.Sprintf(
					"no event persisted for %s (threshold %s)",
					age.Round(time.Second), m.cfg.StaleAfter))
			}
		}
	}

	if m.cfg.MaxQueueDepth > 0 && snap.QueueDepthKnown && snap.QueueDepth > m.cfg.MaxQueueDepth {
		snap.Healthy = false
		snap.Reasons = append(snap.Reasons, fmt.Sprintf(
			"write-queue backlog %d over threshold %d", snap.QueueDepth, m.cfg.MaxQueueDepth))
	}
}

// maybeAlert logs at ERROR when unhealthy, throttled to AlertEvery so a sustained
// outage does not spam (and re-report to BugBarn) every interval. A WAL over the
// warn threshold logs at WARN regardless of overall health, as an early signal.
func (m *Monitor) maybeAlert(ctx context.Context, snap Snapshot) {
	if snap.Healthy {
		if m.cfg.WarnWALBytes > 0 && snap.WALSizeBytes > m.cfg.WarnWALBytes {
			m.logger.Warn("ingest-health: WAL above warning threshold",
				"wal_bytes", snap.WALSizeBytes, "threshold_bytes", m.cfg.WarnWALBytes)
		}
		return
	}

	m.mu.Lock()
	throttled := !m.lastAlert.IsZero() && m.now().Sub(m.lastAlert) < m.cfg.AlertEvery
	if !throttled {
		m.lastAlert = m.now()
	}
	m.mu.Unlock()
	if throttled {
		return
	}

	m.logger.Error("ingest pipeline unhealthy",
		"reasons", snap.Reasons,
		"last_event_age_seconds", snap.LastEventAgeSeconds,
		"queue_depth", snap.QueueDepth,
		"wal_bytes", snap.WALSizeBytes,
	)
	m.notify(ctx, snap)
}

// notify fans the alert out over every configured out-of-band channel. It runs
// on the sample goroutine, bounded by NotifyTimeout, and never lets one dead
// channel stop another: a stalled SMTP server must not swallow the webhook.
func (m *Monitor) notify(ctx context.Context, snap Snapshot) {
	if len(m.notifiers) == 0 {
		return
	}
	// Bounded so a black-holed SMTP host cannot wedge the sample loop; derived
	// from ctx so shutdown aborts delivery rather than waiting it out.
	ctx, cancel := context.WithTimeout(ctx, m.cfg.NotifyTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for _, n := range m.notifiers {
		wg.Add(1)
		go func(n Notifier) {
			defer wg.Done()
			if err := n.Notify(ctx, m.cfg.Environment, snap); err != nil {
				// WARN, not ERROR: an ERROR here would be self-reported through
				// the very pipeline that is already stalled, adding queue
				// pressure and no signal.
				m.logger.Warn("ingest-health: out-of-band alert failed",
					"channel", n.Name(), "error", err)
				return
			}
			m.logger.Info("ingest-health: out-of-band alert sent", "channel", n.Name())
		}(n)
	}
	wg.Wait()
}

func (m *Monitor) registerGauges() {
	meter := tracing.Meter()
	age, err1 := meter.Float64ObservableGauge(
		"bugbarn.ingest.last_event_age_seconds",
		metric.WithDescription("Seconds since the most recently persisted event."),
	)
	wal, err2 := meter.Int64ObservableGauge(
		"bugbarn.ingest.wal_size_bytes",
		metric.WithDescription("Size of the SQLite write-ahead log file."),
	)
	healthy, err3 := meter.Int64ObservableGauge(
		"bugbarn.ingest.healthy",
		metric.WithDescription("1 when the ingest pipeline is healthy, 0 otherwise."),
	)
	if err1 != nil || err2 != nil || err3 != nil {
		return
	}
	reg, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := m.Snapshot()
		if !s.Sampled {
			return nil
		}
		o.ObserveFloat64(age, s.LastEventAgeSeconds)
		o.ObserveInt64(wal, s.WALSizeBytes)
		if s.Healthy {
			o.ObserveInt64(healthy, 1)
		} else {
			o.ObserveInt64(healthy, 0)
		}
		return nil
	}, age, wal, healthy)
	if err == nil {
		m.reg = reg
	}
}

func (m *Monitor) unregisterGauges() {
	if m.reg != nil {
		_ = m.reg.Unregister()
	}
}
