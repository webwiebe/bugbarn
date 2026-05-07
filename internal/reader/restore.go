package reader

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const (
	restoreInterval      = 30 * time.Second
	litestreamConfig     = "/etc/litestream.yml"
	litestreamDBPath     = "/var/lib/bugbarn/bugbarn.db"
	maxLitestreamFailures = 3
)

// StartRestoreLoop periodically restores a fresh SQLite copy from Litestream
// and swaps the Store's read-only connection to the new file.
// After maxLitestreamFailures consecutive failures, it falls back to
// downloading the database directly from the writer pod.
func StartRestoreLoop(ctx context.Context, store *storage.Store, dbPath, writerURL string, logger *slog.Logger) {
	ticker := time.NewTicker(restoreInterval)
	defer ticker.Stop()

	var consecutiveFailures int

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := restoreAndSwap(ctx, store, dbPath, logger); err != nil {
				consecutiveFailures++
				logger.Warn("reader restore failed", "error", err, "consecutive_failures", consecutiveFailures)

				if consecutiveFailures >= maxLitestreamFailures && writerURL != "" {
					logger.Info("litestream restore failed repeatedly, falling back to writer backup")
					if fbErr := fallbackRestoreFromWriter(ctx, store, dbPath, writerURL, logger); fbErr != nil {
						logger.Error("fallback restore from writer failed", "error", fbErr)
					} else {
						consecutiveFailures = 0
					}
				}
			} else {
				consecutiveFailures = 0
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
		litestreamDBPath,
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

func fallbackRestoreFromWriter(ctx context.Context, store *storage.Store, dbPath, writerURL string, logger *slog.Logger) error {
	tmpPath := filepath.Join(filepath.Dir(dbPath), ".bugbarn-writer-backup.db")
	defer os.Remove(tmpPath)

	url := writerURL + "/internal/v1/db-backup"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download from writer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("writer returned %d", resp.StatusCode)
	}

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	n, err := io.Copy(f, resp.Body)
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("empty backup received")
	}

	if err := os.Rename(tmpPath, dbPath); err != nil {
		return fmt.Errorf("rename backup: %w", err)
	}

	if err := store.SwapReadDB(dbPath); err != nil {
		return fmt.Errorf("swap db: %w", err)
	}

	logger.Info("reader database restored from writer backup", "bytes", n)
	return nil
}
