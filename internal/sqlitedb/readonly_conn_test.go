package sqlitedb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestWalPathFromDSN(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"file:/data/bugbarn.db?mode=ro&_pragma=busy_timeout(10000)": "/data/bugbarn.db-wal",
		"file:/data/bugbarn.db":    "/data/bugbarn.db-wal",
		"file:relative.db?mode=ro": "relative.db-wal",
		"plain.db?mode=ro":         "plain.db-wal",
		"plain.db":                 "plain.db-wal",
	}
	for dsn, want := range cases {
		if got := walPathFromDSN(dsn); got != want {
			t.Errorf("walPathFromDSN(%q) = %q, want %q", dsn, got, want)
		}
	}
}

func TestReadOnlyConnectionServesReadsAndRefusesWrites(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ro.db")
	writer := openWriter(t, path)
	growWAL(t, writer)
	reader := openReader(t, path)
	ctx := context.Background()

	if err := reader.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	var n int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&n); err != nil || n != 200 {
		t.Fatalf("count = %d, %v; want 200", n, err)
	}

	// A prepared statement goes through the retrying prepare and query path.
	stmt, err := reader.PrepareContext(ctx, `SELECT name FROM projects WHERE slug = ?`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()
	var name string
	if err := stmt.QueryRowContext(ctx, "proj-7").Scan(&name); err != nil || name != "Project 7" {
		t.Fatalf("prepared query = %q, %v", name, err)
	}

	// The read-only file refuses a write, and the refusal reaches the caller.
	if _, err := reader.ExecContext(ctx, `DELETE FROM projects`); err == nil {
		t.Fatal("a write through a read-only connection must fail")
	}

	// A transaction opens and closes cleanly.
	tx, err := reader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&n); err != nil || n != 200 {
		t.Fatalf("count in tx = %d, %v", n, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// A query that fails for another reason is not retried away.
	if _, err := reader.QueryContext(ctx, `SELECT * FROM missing_table`); err == nil {
		t.Fatal("querying a missing table must fail")
	}
}

func TestIsBusyIgnoresOtherErrors(t *testing.T) {
	t.Parallel()
	if IsBusy(nil) {
		t.Error("nil is not busy")
	}
	if IsBusy(errors.New("database is locked")) {
		t.Error("a flattened error carries no result code and is not recognized")
	}
}

func TestCheckpointDBReportsFramesAndHonoursACanceledContext(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cp.db")
	db := openWriter(t, path)
	growWAL(t, db)

	if frames := CheckpointDB(context.Background(), db, 0, quietLogger()); frames < 0 {
		t.Fatalf("CheckpointDB = %d on a healthy database", frames)
	}
	if size := walSize(t, path); size != 0 {
		t.Fatalf("WAL is %d bytes after a TRUNCATE checkpoint, want 0", size)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := CheckpointDB(ctx, db, 0, quietLogger()); got != -1 {
		t.Fatalf("CheckpointDB with a canceled context = %d, want -1", got)
	}
}
