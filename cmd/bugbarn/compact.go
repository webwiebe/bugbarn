package main

import (
	"context"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// compactStorage runs the one-time storage cleanup BUGBARN_COMPACT_ON_START
// asks for: it deletes Litestream's leftover shadow directory and rewrites
// bugbarn.db in auto_vacuum=INCREMENTAL mode. Both are best-effort; a failure
// is logged and the writer starts on the database as it is.
func compactStorage(ctx context.Context, store *storage.Store, cfg config.Config, logger *slog.Logger) {
	log := logger.With("component", "compact")
	removed, err := storage.RemoveLitestreamShadow(cfg.DBPath)
	if err != nil {
		log.Error("remove litestream shadow directory failed", "error", err)
	} else if removed > 0 {
		log.Info("removed litestream shadow directory", "bytes", removed)
	}

	res, err := store.Compact(ctx, cfg.DBPath, cfg.Compact.MaxLiveBytes)
	if err != nil {
		log.Error("database compaction failed", "error", err)
		return
	}
	if !res.Compacted {
		log.Info("database compaction skipped", "reason", res.Reason,
			"file_bytes", res.BytesBefore, "live_bytes", res.LiveBytes)
		return
	}
	log.Info("database compacted",
		"bytes_before", res.BytesBefore, "bytes_after", res.BytesAfter, "live_bytes", res.LiveBytes)
}
