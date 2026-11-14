package digest

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// BuildNotifiers constructs the list of notifiers from configuration.
func BuildNotifiers(cfg Config) []Notifier {
	var notifiers []Notifier
	if cfg.WebhookURL != "" {
		notifiers = append(notifiers, &WebhookNotifier{URL: cfg.WebhookURL})
	}
	if cfg.Mail.Active() {
		notifiers = append(notifiers, &EmailNotifier{Cfg: cfg.Mail})
	}
	return notifiers
}

// StartScheduler launches a goroutine that fires the digest at the configured
// weekday and hour (UTC). It returns immediately; the goroutine runs until ctx
// is canceled. No-ops if cfg.Enabled() is false.
// If wg is non-nil, it increments the WaitGroup before starting and decrements
// it when the goroutine exits, so callers can wait for a clean shutdown.
func StartScheduler(ctx context.Context, cfg Config, store Store, log *slog.Logger, wg *sync.WaitGroup) {
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "digest")
	if !cfg.Enabled() {
		return
	}
	notifiers := BuildNotifiers(cfg)
	if len(notifiers) == 0 {
		return
	}
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		defer func() {
			if p := recover(); p != nil {
				log.Error("digest scheduler panic", "panic", p)
			}
		}()
		run(ctx, log, cfg, store, notifiers)
	}()
}

func run(ctx context.Context, log *slog.Logger, cfg Config, store Store, notifiers []Notifier) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	runTicks(ctx, log, cfg, store, notifiers, ticker.C)
}

// due reports whether the digest should fire at now: the weekday and hour match
// the configuration (UTC) and the last run was at least 23 hours ago.
func due(cfg Config, now, lastFired time.Time) bool {
	now = now.UTC()
	if int(now.Weekday()) != cfg.Day || now.Hour() != cfg.Hour {
		return false
	}
	return lastFired.IsZero() || now.Sub(lastFired) >= 23*time.Hour
}

// runTicks fires the digest on each tick that is due, until ctx is canceled.
func runTicks(ctx context.Context, log *slog.Logger, cfg Config, store Store, notifiers []Notifier, ticks <-chan time.Time) {
	var lastFired time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticks:
			now = now.UTC()
			if !due(cfg, now, lastFired) {
				continue
			}
			lastFired = now
			log.InfoContext(ctx, "sending weekly digest")
			// Send owns its own gather/delivery budgets; ctx here only carries
			// shutdown cancellation.
			errs := Send(ctx, log, cfg, store, notifiers)
			// Send already logged each failure at ERROR; this line only closes the run.
			if len(errs) == 0 {
				log.InfoContext(ctx, "digest sent")
			} else {
				log.InfoContext(ctx, "digest finished with failures", "failures", len(errs))
			}
		}
	}
}
