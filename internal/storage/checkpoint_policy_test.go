package storage

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pinSnapshot opens a reader on dbPath, starts a read transaction that pins a WAL
// snapshot, then moves the WAL past it with writes through store. That is the
// production shape (reader pods holding snapshots) that makes a TRUNCATE
// checkpoint wait in SQLite's busy handler. The transaction is the only thing
// that blocks: nothing here loops or generates load. release ends the snapshot.
func pinSnapshot(t *testing.T, store *Store, dbPath string) (release func()) {
	t.Helper()
	growWAL(t, store)
	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	tx, err := reader.readDB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read tx: %v", err)
	}
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM projects`).Scan(&n); err != nil {
		t.Fatalf("reader query: %v", err)
	}
	for i := 0; i < 50; i++ {
		if _, err := store.db.Exec(
			`INSERT INTO projects (slug, name, status, issue_prefix) VALUES (?, ?, 'active', 'Q')`,
			fmt.Sprintf("post-snap-%d", i), fmt.Sprintf("Post Snapshot %d", i),
		); err != nil {
			t.Fatalf("post-snapshot write %d: %v", i, err)
		}
	}
	return func() {
		_ = tx.Rollback()
		_ = reader.Close()
	}
}

// bugbarn#236: a TRUNCATE attempt that a reader snapshot blocks must hand the
// write connection back after the policy's wait, not after the connection's 10s
// busy_timeout. Production stalled 12 to 28s here and failed forwarded check-ins.
func TestCheckpointAttemptStallIsBoundedByPolicyBusyTimeout(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	release := pinSnapshot(t, store, dbPath)
	defer release()

	wait := DefaultCheckpointPolicy().TruncateBusyTimeout
	start := time.Now()
	busy, _, _, err := checkpointAttempt(context.Background(), store.db, "TRUNCATE", wait)
	stall := time.Since(start)
	if err != nil {
		t.Fatalf("checkpoint attempt: %v", err)
	}
	if busy != 1 {
		t.Fatalf("busy = %d, want 1: the held snapshot did not block TRUNCATE, so this test proves nothing", busy)
	}
	// The write connection is held for the whole attempt. Ceiling: the wait plus
	// one second of slack, an order of magnitude under the 10s it used to take.
	if limit := wait + time.Second; stall > limit {
		t.Errorf("TRUNCATE held the write connection for %v, want at most %v", stall, limit)
	}

	// The shortened wait must not leak into the pooled connection: every other
	// writer relies on the 10s busy_timeout from sqliteDSN.
	var got int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&got); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if got != 10000 {
		t.Errorf("busy_timeout after the attempt = %dms, want 10000", got)
	}
}

// The other side of the bound: a short wait only helps if the WAL still shrinks.
// PASSIVE ticks never reset the file; the Nth tick does, once the snapshot that
// blocked it is gone. This is the 377MB case from 2026-07-16.
func TestWALTruncatesWithinNTicksAfterSnapshotReleased(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	release := pinSnapshot(t, store, dbPath)

	policy := CheckpointPolicy{TruncateEvery: 3, TruncateBusyTimeout: 100 * time.Millisecond}
	runTick := func(tick int) {
		t.Helper()
		// 500ms: one 100ms attempt, then the 5s retry wait is cut by the deadline.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		checkpointTick(ctx, store.db, tick, &checkpointState{}, policy, quietLogger())
	}

	// Ticks 1 and 2 are PASSIVE, tick 3 is TRUNCATE and the snapshot blocks it.
	for tick := 1; tick <= 3; tick++ {
		runTick(tick)
	}
	if got := walSize(t, dbPath); got == 0 {
		t.Fatal("WAL was truncated while a reader snapshot was held; the scenario does not block TRUNCATE")
	}

	release()

	// Ticks 4 and 5 are PASSIVE and leave the file alone; tick 6 truncates.
	for tick := 4; tick <= 5; tick++ {
		runTick(tick)
		if got := walSize(t, dbPath); got == 0 {
			t.Fatalf("tick %d (PASSIVE) truncated the WAL; TruncateEvery is not honored", tick)
		}
	}
	runTick(6)
	if got := walSize(t, dbPath); got != 0 {
		t.Errorf("WAL is %d bytes after the Nth tick with no snapshot held, want 0", got)
	}
}

// LegacyCheckpointPolicy must stay what production ran before bugbarn#236, so the
// env knobs can restore it: TRUNCATE on every tick.
func TestLegacyPolicyTruncatesEveryTick(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	growWAL(t, store)

	checkpointTick(context.Background(), store.db, 1, &checkpointState{}, LegacyCheckpointPolicy(), quietLogger())
	if got := walSize(t, dbPath); got != 0 {
		t.Errorf("WAL is %d bytes after one legacy tick, want 0", got)
	}
}

// A WAL that stops truncating must file an issue in BugBarn itself: the size
// report logs at Error above WarnBytes (selflog captures Error) and stays below
// Error otherwise.
func TestReportWALSizeLogsErrorAboveThreshold(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	size := int64(100)
	policy := CheckpointPolicy{
		WALSize:   func() (int64, error) { return size, nil },
		WarnBytes: 1000,
	}

	reportWALSize(policy, log)
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("below the threshold must not log at Error: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"wal_bytes":100`) {
		t.Errorf("size not logged: %s", buf.String())
	}

	buf.Reset()
	size = 1001
	reportWALSize(policy, log)
	if !strings.Contains(buf.String(), `"level":"ERROR"`) || !strings.Contains(buf.String(), `"wal_bytes":1001`) {
		t.Errorf("above the threshold must log at Error with the size: %s", buf.String())
	}

	buf.Reset()
	size = 1000
	reportWALSize(policy, log)
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("exactly at the threshold must not log at Error: %s", buf.String())
	}

	buf.Reset()
	reportWALSize(CheckpointPolicy{}, log)
	if buf.Len() != 0 {
		t.Errorf("no size source must log nothing: %s", buf.String())
	}
}

func TestWALFileSizeMissingIsZero(t *testing.T) {
	got, err := WALFileSize(filepath.Join(t.TempDir(), "none.db"))()
	if err != nil || got != 0 {
		t.Errorf("missing WAL = %d, %v; want 0, nil", got, err)
	}
}

func TestCheckpointPolicyDefaults(t *testing.T) {
	got := CheckpointPolicy{}.normalized()
	want := DefaultCheckpointPolicy()
	if got.TruncateEvery != want.TruncateEvery || got.TruncateBusyTimeout != want.TruncateBusyTimeout {
		t.Errorf("zero policy normalizes to %+v, want %+v", got, want)
	}
	if want.TruncateEvery != 10 || want.TruncateBusyTimeout != time.Second {
		t.Errorf("default policy = %+v, want TRUNCATE every 10 ticks with a 1s wait", want)
	}
	legacy := LegacyCheckpointPolicy()
	if legacy.TruncateEvery != 1 || legacy.TruncateBusyTimeout != 10*time.Second {
		t.Errorf("legacy policy = %+v, want TRUNCATE every tick with a 10s wait", legacy)
	}
}
