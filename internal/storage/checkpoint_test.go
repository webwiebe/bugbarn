package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// TestCheckpointTruncatesWAL verifies that with wal_autocheckpoint(0) the WAL
// grows under writes and is reclaimed by an explicit TRUNCATE checkpoint. This
// is the mechanism that keeps the on-disk WAL bounded without relying on
// Litestream's (out-of-process, lock-losing) checkpoint.
func TestCheckpointTruncatesWAL(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	projectID := store.defaultProjectID

	// Write enough log entries to grow the WAL well past empty. With
	// wal_autocheckpoint(0) nothing reclaims it until we checkpoint explicitly.
	entries := make([]domain.LogEntry, 2000)
	for i := range entries {
		entries[i] = domain.LogEntry{
			ProjectID:  projectID,
			ReceivedAt: time.Now().UTC(),
			LevelNum:   30,
			Level:      "info",
			Message:    "checkpoint test log line padding padding padding",
		}
	}
	if err := store.InsertLogEntries(ctx, entries); err != nil {
		t.Fatalf("InsertLogEntries: %v", err)
	}

	walPath := dbPath + "-wal"
	grown := walSize(t, walPath)
	if grown == 0 {
		t.Fatal("expected WAL to grow under writes with wal_autocheckpoint(0)")
	}

	// No concurrent readers in this test, so TRUNCATE should fully succeed and
	// shrink the WAL file back to (near) zero.
	store.FinalCheckpoint(nil)

	after := walSize(t, walPath)
	if after >= grown {
		t.Fatalf("expected WAL to shrink after checkpoint: before=%d after=%d", grown, after)
	}
}

func walSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}
