package analytics

import (
	"context"
	"log/slog"
	"sync"
	"time"
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
func StartWorker(ctx context.Context, store Store, retentionDays int, wg *sync.WaitGroup) {
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
				slog.Error("analytics worker panic", "panic", p)
			}
		}()
		runRollup(ctx, store, retentionDays)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				runRollup(ctx, store, retentionDays)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func runRollup(ctx context.Context, store Store, retentionDays int) {
	projectIDs, err := store.ListProjectIDs(ctx)
	if err != nil {
		slog.Error("analytics rollup: failed to list projects", "error", err)
		return
	}

	// Roll up the past 2 days (yesterday + day before) to catch any gaps.
	now := time.Now().UTC()
	dates := []time.Time{
		now.AddDate(0, 0, -1).Truncate(24 * time.Hour),
		now.AddDate(0, 0, -2).Truncate(24 * time.Hour),
	}

	for _, pid := range projectIDs {
		for _, date := range dates {
			if err := store.RollupDailyAnalytics(ctx, pid, date); err != nil {
				slog.Error("analytics rollup: failed to roll up project", "project_id", pid, "date", date.Format("2006-01-02"), "error", err)
			}
		}
	}

	cutoff := now.AddDate(0, 0, -retentionDays)
	if err := store.DeleteOldPageviews(ctx, cutoff); err != nil {
		slog.Error("analytics retention: failed to delete old pageviews", "error", err)
	}
}
