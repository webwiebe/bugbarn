package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// growWAL writes enough rows to produce a non-trivial WAL. With
// wal_autocheckpoint(0) nothing reclaims it, so it only grows.
func growWAL(t *testing.T, s *Store) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, err := s.db.Exec(
			`INSERT INTO projects (slug, name, status, issue_prefix) VALUES (?, ?, 'active', 'P')`,
			fmt.Sprintf("proj-%d", i), fmt.Sprintf("Project %d", i),
		); err != nil {
			t.Fatalf("seed write %d: %v", i, err)
		}
	}
}

func walSize(t *testing.T, dbPath string) int64 {
	t.Helper()
	fi, err := os.Stat(dbPath + "-wal")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("stat wal: %v", err)
	}
	return fi.Size()
}

// The core of the fix: a TRUNCATE checkpoint must actually reset the WAL. With
// wal_autocheckpoint(0) and no Litestream, this loop is the only thing standing
// between us and unbounded WAL growth.
func TestCheckpointTruncatesWAL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	growWAL(t, store)
	before := walSize(t, dbPath)
	if before == 0 {
		t.Fatal("expected a non-empty WAL after writes; test cannot prove truncation")
	}

	if frames := store.checkpoint(context.Background(), 0, quietLogger()); frames != 0 {
		t.Errorf("checkpoint returned %d WAL frames, want 0 (fully truncated)", frames)
	}

	after := walSize(t, dbPath)
	if after != 0 {
		t.Errorf("WAL size after TRUNCATE checkpoint = %d, want 0 (was %d before)", after, before)
	}
}

// A read-only connection on the same file is exactly the production shape (the
// reader pods). The checkpoint must still eventually truncate rather than
// silently give up the way a PASSIVE checkpoint does.
func TestCheckpointTruncatesWALWithReaderOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	growWAL(t, store)

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()
	var n int
	if err := reader.readDB().QueryRow(`SELECT count(*) FROM projects`).Scan(&n); err != nil {
		t.Fatalf("reader query: %v", err)
	}

	if frames := store.checkpoint(context.Background(), 10*time.Millisecond, quietLogger()); frames != 0 {
		t.Errorf("checkpoint with reader open returned %d frames, want 0", frames)
	}
	if got := walSize(t, dbPath); got != 0 {
		t.Errorf("WAL size with reader open = %d, want 0", got)
	}
}

func TestRunPeriodicCheckpointStopsOnContextCancel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	growWAL(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		store.RunPeriodicCheckpoint(ctx, 5*time.Millisecond, quietLogger())
		close(done)
	}()

	// Poll rather than sleep a fixed amount: under parallel test load the
	// goroutine may not be scheduled for a while, and asserting on a fixed sleep
	// makes this flaky.
	deadline := time.Now().Add(5 * time.Second)
	truncated := false
	for time.Now().Before(deadline) {
		if walSize(t, dbPath) == 0 {
			truncated = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !truncated {
		t.Errorf("periodic checkpoint did not truncate the WAL within 5s (size %d)", walSize(t, dbPath))
	}

	// The loop must exit promptly once the context is canceled.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunPeriodicCheckpoint did not return after context cancel")
	}
}

// A read-only store has no write connection; the checkpointer must no-op rather
// than panic, since reader pods construct a Store too.
func TestRunPeriodicCheckpointNoopOnReadOnlyStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	// autoMigrate=false: Open's background fingerprint migration would race the
	// immediate Close below and log a spurious error.
	store, err := open(dbPath, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	store.Close()

	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	done := make(chan struct{})
	go func() {
		// Must return immediately despite the long interval.
		reader.RunPeriodicCheckpoint(context.Background(), time.Hour, quietLogger())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPeriodicCheckpoint on a read-only store should return immediately")
	}
	reader.FinalCheckpoint(quietLogger()) // must not panic
}
