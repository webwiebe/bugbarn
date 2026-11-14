package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

const (
	// ReleasesPerProject is how many release rows each project keeps. A deploy
	// pipeline posts a few markers per release and events add one row per new
	// release value, so this holds months of history while bounding what one
	// project's ingest key can store.
	ReleasesPerProject = 1000

	// maxReleaseBatches bounds one sweep's release pass; a larger backlog is
	// worked off over the next sweeps.
	maxReleaseBatches = 50
)

// sweepReleases deletes the release rows beyond each project's newest
// ReleasesPerProject, in batches with a pause between them so the writer stays
// free for ingest. A failed batch loses nothing, so it is a warning; the next
// sweep tries again.
func sweepReleases(ctx context.Context, store Store, tun tuning, log *slog.Logger) {
	start := time.Now()
	var deleted int64
	for range maxReleaseBatches {
		if ctx.Err() != nil {
			return
		}
		n, err := store.PruneReleases(ctx, ReleasesPerProject, tun.batchSize)
		if err != nil {
			if !apperr.IsContextError(err) {
				log.Warn("retention: release prune failed", "deleted_so_far", deleted, "error", err)
			}
			return
		}
		deleted += n
		if n < int64(tun.batchSize) {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tun.minPause):
		}
	}
	if deleted > 0 {
		log.Info("retention: pruned release rows",
			"deleted", deleted,
			"kept_per_project", ReleasesPerProject,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}
