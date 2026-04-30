package analytics

import (
	"context"
	"log"
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
func StartWorker(ctx context.Context, store Store, retentionDays int) {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	go func() {
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
		log.Printf("analytics rollup: list projects: %v", err)
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
				log.Printf("analytics rollup: project %d date %s: %v", pid, date.Format("2006-01-02"), err)
			}
		}
	}

	cutoff := now.AddDate(0, 0, -retentionDays)
	if err := store.DeleteOldPageviews(ctx, cutoff); err != nil {
		log.Printf("analytics retention: delete old pageviews: %v", err)
	}
}
