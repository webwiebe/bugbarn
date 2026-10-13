// Package silence raises an alert when a project that used to report errors stops
// reporting. A broken reporter and a healthy product look the same from the
// error list alone, so the rule is narrow: several distinct issues were active
// in the lookback window and none has been seen for the silence period.
package silence

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Config sets the silence rule.
type Config struct {
	// SilentFor is how long a project must have reported nothing.
	SilentFor time.Duration
	// Lookback is the window before SilentFor in which activity counts.
	Lookback time.Duration
	// MinEarlier is how many issues must have been active in that window.
	MinEarlier int
	// Interval is how often the rule is evaluated.
	Interval time.Duration
}

// DefaultConfig alerts after 7 quiet days for projects with 3 or more issues
// active in the 30 days before that.
func DefaultConfig() Config {
	return Config{SilentFor: 7 * 24 * time.Hour, Lookback: 30 * 24 * time.Hour, MinEarlier: 3, Interval: time.Hour}
}

// Source supplies per-project activity counts.
type Source interface {
	ProjectActivity(ctx context.Context, now time.Time, silentFor, lookback time.Duration) ([]storage.ProjectActivity, error)
}

// Notifier delivers the alert for one silent project.
type Notifier interface {
	NotifySilent(ctx context.Context, slug string, silentFor time.Duration) error
}

// Monitor evaluates the silence rule. It alerts once per silent episode; the
// episode ends when the project reports again. State is in memory, so a restart
// repeats an alert at most once per silent project.
type Monitor struct {
	cfg      Config
	src      Source
	notifier Notifier
	log      *slog.Logger

	mu    sync.Mutex
	alert map[int64]bool
}

// New builds a Monitor.
func New(cfg Config, src Source, notifier Notifier, log *slog.Logger) *Monitor {
	return &Monitor{cfg: cfg, src: src, notifier: notifier, log: log, alert: map[int64]bool{}}
}

// Check evaluates every project once.
func (m *Monitor) Check(ctx context.Context) {
	activity, err := m.src.ProjectActivity(ctx, time.Now(), m.cfg.SilentFor, m.cfg.Lookback)
	if err != nil {
		m.log.Error("silence check: read project activity", "error", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range activity {
		if a.Recent > 0 {
			delete(m.alert, a.ProjectID)
			continue
		}
		if a.Earlier < m.cfg.MinEarlier || m.alert[a.ProjectID] {
			continue
		}
		if err := m.notifier.NotifySilent(ctx, a.Slug, m.cfg.SilentFor); err != nil {
			m.log.Error("silence alert delivery failed", "project", a.Slug, "error", err)
			continue
		}
		m.alert[a.ProjectID] = true
		m.log.Info("silence alert sent", "project", a.Slug, "silent_for", m.cfg.SilentFor.String())
	}
}

// Start runs Check on the configured interval until ctx ends.
func (m *Monitor) Start(ctx context.Context, wg *sync.WaitGroup) {
	interval := m.cfg.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Check(ctx)
			}
		}
	}()
}
