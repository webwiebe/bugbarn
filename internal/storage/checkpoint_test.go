package storage

import (
	"context"
	"database/sql"
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

// The bound added for #166: a checkpoint that keeps coming back busy must give
// up when its context expires instead of retrying forever. RunPeriodicCheckpoint
// relies on exactly this to cap each tick's share of the single write
// connection, so if this contract breaks the unbounded retry is back.
func TestCheckpointGivesUpWhenPersistentlyBusy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	growWAL(t, store)

	// An OPEN read transaction pins a WAL snapshot for as long as it lives,
	// which is what blocks TRUNCATE backfill. (The reader in the sibling test
	// finishes its query, so it holds nothing and truncation succeeds.)
	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()
	tx, err := reader.readDB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read tx: %v", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM projects`).Scan(&n); err != nil {
		t.Fatalf("reader query: %v", err)
	}
	// Move the WAL past the pinned snapshot. Distinct slugs: growWAL's rows are
	// already in the table.
	for i := 0; i < 200; i++ {
		if _, err := store.db.Exec(
			`INSERT INTO projects (slug, name, status, issue_prefix) VALUES (?, ?, 'active', 'Q')`,
			fmt.Sprintf("post-snap-%d", i), fmt.Sprintf("Post Snapshot %d", i),
		); err != nil {
			t.Fatalf("post-snapshot write %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	frames := store.checkpoint(ctx, 50*time.Millisecond, quietLogger())
	elapsed := time.Since(start)

	if frames == 0 {
		t.Skip("checkpoint truncated despite a held read snapshot; busy path not reproduced here")
	}
	// Ceiling allows ONE full attempt, not a precise duration. A blocked
	// attempt sits in SQLite's busy handler for up to busy_timeout(10s) and is
	// not interruptible by the Go context, so the deadline takes effect at the
	// next retry decision rather than mid-pragma. What matters is that the call
	// returns at all: without a deadline it retries forever while the reader
	// holds its snapshot, and this test would hang instead of failing.
	if elapsed > 20*time.Second {
		t.Errorf("checkpoint ran %v under persistent busy, want it to give up after ~one attempt", elapsed)
	}
}

// The per-tick budget only bounds contention if it is a fraction of the tick
// interval; a budget >= the interval would mean back-to-back checkpointing.
func TestCheckpointTickBudgetIsAFractionOfTheInterval(t *testing.T) {
	if checkpointTickBudget >= DefaultCheckpointInterval {
		t.Errorf("checkpointTickBudget %v >= DefaultCheckpointInterval %v: a tick could occupy the write connection continuously",
			checkpointTickBudget, DefaultCheckpointInterval)
	}
	if checkpointTickBudget <= checkpointRetryInterval {
		t.Errorf("checkpointTickBudget %v <= checkpointRetryInterval %v: no retry would ever get a second attempt",
			checkpointTickBudget, checkpointRetryInterval)
	}
}
