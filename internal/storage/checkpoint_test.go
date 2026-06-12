package storage

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestCheckpointTruncatesWAL verifies the core of the WAL-growth fix: with
// wal_autocheckpoint disabled, a burst of writes grows the -wal sidecar, and an
// explicit TRUNCATE checkpoint drains it back to zero. This is the behaviour that
// keeps the production WAL bounded under sustained load.
func TestCheckpointTruncatesWAL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// autocheckpoint must be off, otherwise SQLite would silently truncate the
	// WAL on its own and this test (and the production fix) would be meaningless.
	var autockpt int
	if err := store.db.QueryRowContext(ctx, "PRAGMA wal_autocheckpoint").Scan(&autockpt); err != nil {
		t.Fatalf("read wal_autocheckpoint: %v", err)
	}
	if autockpt != 0 {
		t.Fatalf("wal_autocheckpoint = %d, want 0 (auto-checkpoint must be disabled)", autockpt)
	}

	// Write enough rows that the WAL grows past empty. log_entries is a plain
	// insert path with no read snapshot held open, so the checkpoint can fully
	// truncate afterwards.
	entries := make([]LogEntry, 0, 500)
	for i := 0; i < 500; i++ {
		entries = append(entries, LogEntry{
			ProjectID: store.DefaultProjectID(),
			Level:     "error",
			LevelNum:  3,
			Message:   "checkpoint test payload to grow the write-ahead log",
		})
	}
	if err := store.InsertLogEntries(ctx, entries); err != nil {
		t.Fatalf("InsertLogEntries: %v", err)
	}

	walPath := dbPath + "-wal"
	if sz := walSize(t, walPath); sz == 0 {
		t.Fatalf("WAL is empty after inserts; expected it to grow with autocheckpoint off")
	}

	store.Checkpoint(ctx, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if sz := walSize(t, walPath); sz != 0 {
		t.Errorf("WAL size after TRUNCATE checkpoint = %d, want 0", sz)
	}
}

func walSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Size()
}
