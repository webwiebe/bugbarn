package analytics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// Store is the subset of storage.Store used by the analytics worker.
type Store interface {
	ListProjectIDs(ctx context.Context) ([]int64, error)
	RollupDailyAnalytics(ctx context.Context, projectID int64, date time.Time) error
	DeleteOldPageviews(ctx context.Context, cutoff time.Time) error
}

// StartWorker rolls up raw page-view data into analytics_daily on a 1-hour
// cadence. It performs an initial run on startup to catch any missed rollups.
// If wg is non-nil, it increments the WaitGroup before starting and decrements
// it when the goroutine exits, so callers can wait for a clean shutdown.
func StartWorker(ctx context.Context, store Store, retentionDays int, log *slog.Logger, wg *sync.WaitGroup) {
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "analytics")
	if retentionDays <= 0 {
		retentionDays = 90
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
				log.Error("analytics worker panic", "panic", p)
			}
		}()
		runRollup(ctx, log, store, retentionDays)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				runRollup(ctx, log, store, retentionDays)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func runRollup(ctx context.Context, log *slog.Logger, store Store, retentionDays int) {
	projectIDs, err := store.ListProjectIDs(ctx)
	if err != nil {
		apperr.LogFailure(ctx, log, "analytics rollup: failed to list projects", err)
		return
	}

	// Roll up the past 2 days (yesterday + day before) to catch any gaps.
	now := time.Now().UTC()
	dates := []time.Time{
		now.AddDate(0, 0, -1).Truncate(24 * time.Hour),
		now.AddDate(0, 0, -2).Truncate(24 * time.Hour),
	}

	for _, pid := range projectIDs {
		// Stop at the first sign of shutdown rather than walking the rest of
		// the fleet just to fail on every one of them.
		if ctx.Err() != nil {
			log.InfoContext(ctx, "analytics rollup: stopping early, context done", "error", ctx.Err())
			return
		}
		for _, date := range dates {
			if err := store.RollupDailyAnalytics(ctx, pid, date); err != nil {
				apperr.LogFailure(ctx, log, "analytics rollup: failed to roll up project", err,
					"project_id", pid, "date", date.Format("2006-01-02"))
			}
		}
	}

	cutoff := now.AddDate(0, 0, -retentionDays)
	if err := store.DeleteOldPageviews(ctx, cutoff); err != nil {
		apperr.LogFailure(ctx, log, "analytics retention: failed to delete old pageviews", err)
	}
}
