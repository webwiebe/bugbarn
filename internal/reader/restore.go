package reader

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const (
	restoreInterval   = 30 * time.Second
	litestreamConfig  = "/etc/litestream.yml"
)

// StartRestoreLoop periodically restores a fresh SQLite copy from Litestream
// and swaps the Store's read-only connection to the new file.
func StartRestoreLoop(ctx context.Context, store *storage.Store, dbPath string, logger *slog.Logger) {
	ticker := time.NewTicker(restoreInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := restoreAndSwap(ctx, store, dbPath, logger); err != nil {
				logger.Warn("reader restore failed", "error", err)
			}
		}
	}
}

func restoreAndSwap(ctx context.Context, store *storage.Store, dbPath string, logger *slog.Logger) error {
	tmpPath := filepath.Join(filepath.Dir(dbPath), ".bugbarn-restore.db")
	defer os.Remove(tmpPath)

	cmd := exec.CommandContext(ctx, "litestream", "restore",
		"-config", litestreamConfig,
		"-if-replica-exists",
		"-o", tmpPath,
		dbPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}

	if _, err := os.Stat(tmpPath); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, dbPath); err != nil {
		return err
	}

	if err := store.SwapReadDB(dbPath); err != nil {
		return err
	}

	logger.Debug("reader database refreshed from replica")
	return nil
}
