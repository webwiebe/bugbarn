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
	if cfg.Mail.active() {
		notifiers = append(notifiers, &EmailNotifier{Cfg: cfg.Mail})
	}
	return notifiers
}

// StartScheduler launches a goroutine that fires the digest at the configured
// weekday and hour (UTC). It returns immediately; the goroutine runs until ctx
// is cancelled. No-ops if cfg.Enabled() is false.
// If wg is non-nil, it increments the WaitGroup before starting and decrements
// it when the goroutine exits, so callers can wait for a clean shutdown.
func StartScheduler(ctx context.Context, cfg Config, store Store, wg *sync.WaitGroup) {
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
				slog.Error("digest scheduler panic", "panic", p)
			}
		}()
		run(ctx, cfg, store, notifiers)
	}()
}

func run(ctx context.Context, cfg Config, store Store, notifiers []Notifier) {
	var lastFired time.Time

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			now = now.UTC()
			if int(now.Weekday()) != cfg.Day || now.Hour() != cfg.Hour {
				continue
			}
			if !lastFired.IsZero() && now.Sub(lastFired) < 23*time.Hour {
				continue
			}
			lastFired = now
			slog.Info("digest: sending weekly digest")
			digestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			errs := Send(digestCtx, cfg, store, notifiers)
			cancel()
			for _, err := range errs {
				slog.Error("digest: delivery error", "error", err)
			}
			if len(errs) == 0 {
				slog.Info("digest: sent successfully")
			}
		}
	}
}
