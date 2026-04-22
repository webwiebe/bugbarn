package digest

import (
	"context"
	"log"
	"time"
)

// StartScheduler launches a goroutine that fires the digest at the configured
// weekday and hour (UTC). It returns immediately; the goroutine runs until ctx
// is cancelled. No-ops if cfg.Enabled() is false.
func StartScheduler(ctx context.Context, cfg Config, store Store) {
	if !cfg.Enabled() {
		return
	}
	go run(ctx, cfg, store)
}

func run(ctx context.Context, cfg Config, store Store) {
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
			// Guard against re-firing within the same hour (ticker fires ~60 times/hour).
			if !lastFired.IsZero() && now.Sub(lastFired) < 23*time.Hour {
				continue
			}
			lastFired = now
			log.Printf("digest: sending weekly digest")
			digestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			errs := Send(digestCtx, cfg, store)
			cancel()
			for _, err := range errs {
				log.Printf("digest: %v", err)
			}
			if len(errs) == 0 {
				log.Printf("digest: sent successfully")
			}
		}
	}
}
