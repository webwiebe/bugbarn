package reader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const (
	restoreInterval       = 30 * time.Second
	litestreamConfig      = "/etc/litestream.yml"
	litestreamDBPath      = "/var/lib/bugbarn/bugbarn.db"
	maxLitestreamFailures = 3
)

// StartRestoreLoop periodically restores a fresh SQLite copy from Litestream
// and swaps the Store's read-only connection to the new file.
// After maxLitestreamFailures consecutive failures that cannot be resolved via
// snapshot restore, it falls back to downloading the database directly from the
// writer (requires writerURL; only available in deployments where the writer is
// network-accessible).
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
					logger.Warn("litestream restore failed repeatedly, falling back to writer backup")
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

	stderr, err := runLitestreamRestore(ctx, tmpPath, "")
	if err != nil {
		if !isWALChainError(stderr) {
			return err
		}
		// WAL chain is broken (missing segment in S3). The snapshot that was
		// taken after the gap already incorporates those frames, so restore
		// directly to the latest snapshot's timestamp rather than trying to
		// replay the full chain.
		logger.Warn("WAL chain broken, retrying restore from latest snapshot", "error", err)
		ts, tsErr := latestSnapshotTime(ctx)
		if tsErr != nil {
			return fmt.Errorf("WAL chain broken and no usable snapshot found: %w", err)
		}
		if _, snapshotErr := runLitestreamRestore(ctx, tmpPath, ts.UTC().Format(time.RFC3339)); snapshotErr != nil {
			return fmt.Errorf("snapshot restore also failed: %w", snapshotErr)
		}
		logger.Info("reader database restored from latest snapshot after WAL chain failure", "snapshot_time", ts)
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

// runLitestreamRestore runs litestream restore and returns (stderr output, error).
// When timestamp is non-empty, -timestamp is passed to restore to that exact point,
// which lets Litestream use a known-good snapshot without needing intact WAL before it.
func runLitestreamRestore(ctx context.Context, tmpPath, timestamp string) (string, error) {
	args := []string{"restore",
		"-config", litestreamConfig,
		"-if-replica-exists",
		"-o", tmpPath,
	}
	if timestamp != "" {
		args = append(args, "-timestamp", timestamp)
	}
	args = append(args, litestreamDBPath)

	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "litestream", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	err := cmd.Run()
	return buf.String(), err
}

// isWALChainError reports whether litestream stderr indicates a broken WAL chain
// rather than a total replica absence.
func isWALChainError(stderr string) bool {
	return strings.Contains(stderr, "missing initial wal segment") ||
		strings.Contains(stderr, "missing wal segment") ||
		strings.Contains(stderr, "cannot find max wal index")
}

// latestSnapshotTime returns the creation time of the most recent Litestream snapshot.
func latestSnapshotTime(ctx context.Context) (time.Time, error) {
	cmd := exec.CommandContext(ctx, "litestream", "snapshots",
		"-config", litestreamConfig,
		litestreamDBPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("list snapshots: %w", err)
	}

	var latest time.Time
	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] { // skip header row
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		t, err := time.Parse(time.RFC3339, fields[4])
		if err != nil {
			continue
		}
		if t.After(latest) {
			latest = t
		}
	}

	if latest.IsZero() {
		return time.Time{}, fmt.Errorf("no snapshots found")
	}
	return latest, nil
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
