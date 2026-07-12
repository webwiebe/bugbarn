package sessionstore

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// pruneInterval is how often expired web sessions are removed. Expiry is also
// enforced per request, so pruning is pure hygiene (bounds table growth).
const pruneInterval = time.Hour

// Pruner is the narrow storage surface the background pruner needs.
type Pruner interface {
	PruneWebSessions(ctx context.Context, now time.Time) (int64, error)
}

// StartPruner runs the hourly web-session prune loop until ctx is canceled.
// Writer/single-process only — readers have no write connection.
func StartPruner(ctx context.Context, db Pruner, logger *slog.Logger, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(pruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := db.PruneWebSessions(ctx, time.Now().UTC()); err != nil {
					logger.Error("web-session prune failed", "error", err)
				} else if n > 0 {
					logger.Info("pruned expired web sessions", "count", n)
				}
			}
		}
	}()
}
