package monitorwatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Config sets how the detector runs.
type Config struct {
	// Interval is how often monitors are scanned.
	Interval time.Duration
	// StartupGrace is how long after a start no monitor is marked missed, so a
	// deploy or an outage of BugBarn itself does not blame the jobs.
	StartupGrace time.Duration
}

// DefaultConfig scans every 30 seconds and waits 5 minutes after a start.
func DefaultConfig() Config {
	return Config{Interval: 30 * time.Second, StartupGrace: 5 * time.Minute}
}

// Store is the part of the store the detector needs.
type Store interface {
	ListEnabledMonitors(ctx context.Context) ([]domain.Monitor, error)
	MarkMonitorMissed(ctx context.Context, id int64, seenCheckIn, now time.Time) (bool, error)
	MarkMonitorEmitted(ctx context.Context, id int64, now time.Time) error
}

// MissReporter reports one missed monitor.
type MissReporter interface {
	Missed(ctx context.Context, m domain.Monitor) error
}

// Detector finds monitors that stayed silent past interval and grace and
// reports them. It runs on the writer only, which owns the database.
type Detector struct {
	cfg      Config
	store    Store
	reporter MissReporter
	log      *slog.Logger
	now      func() time.Time
	started  time.Time
}

// NewDetector builds a detector. The startup grace counts from this call.
func NewDetector(cfg Config, store Store, reporter MissReporter, log *slog.Logger) *Detector {
	d := &Detector{cfg: cfg, store: store, reporter: reporter, log: log.With("component", "monitors"), now: time.Now}
	d.started = d.now()
	return d
}

// SetClock replaces the time source and restarts the startup grace from it.
func (d *Detector) SetClock(now func() time.Time) {
	d.now = now
	d.started = now()
}

// Start runs the scan loop until ctx ends.
func (d *Detector) Start(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(d.cfg.Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.Check(ctx)
			}
		}
	}()
}

// Check scans every enabled monitor once. An ok monitor past its due time
// becomes missed and is reported; a missed monitor whose report is pending or
// a day old is reported again.
func (d *Detector) Check(ctx context.Context) {
	monitors, err := d.store.ListEnabledMonitors(ctx)
	if err != nil {
		d.log.Error("monitor check: list monitors", "error", err)
		return
	}
	now := d.now()
	armed := now.Sub(d.started) >= d.cfg.StartupGrace
	for _, m := range monitors {
		if ctx.Err() != nil {
			return
		}
		switch m.State {
		case domain.MonitorStateOK:
			if armed && now.After(m.NextDue()) {
				d.markMissed(ctx, m, now)
			}
		case domain.MonitorStateMissed:
			if m.NeedsReport(now) {
				d.report(ctx, m, now)
			}
		}
	}
}

func (d *Detector) markMissed(ctx context.Context, m domain.Monitor, now time.Time) {
	applied, err := d.store.MarkMonitorMissed(ctx, m.ID, m.LastCheckInAt, now)
	if err != nil {
		d.log.Error("monitor check: mark missed", "monitor", m.Slug, "project", m.ProjectSlug, "error", err)
		return
	}
	if !applied {
		return // a check-in landed since the scan
	}
	m.State, m.StateSince = domain.MonitorStateMissed, now
	d.report(ctx, m, now)
}

func (d *Detector) report(ctx context.Context, m domain.Monitor, now time.Time) {
	if err := d.reporter.Missed(ctx, m); err != nil {
		if ctx.Err() != nil {
			return
		}
		// Nothing marks the report as sent, so the next scan tries again.
		d.log.Error("monitor missed but not reported", "monitor", m.Slug, "project", m.ProjectSlug, "error", err)
		return
	}
	if err := d.store.MarkMonitorEmitted(ctx, m.ID, now); err != nil {
		d.log.Error("monitor report not recorded", "monitor", m.Slug, "project", m.ProjectSlug, "error", err)
	}
}
