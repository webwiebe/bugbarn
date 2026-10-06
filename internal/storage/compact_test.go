package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fillAndFree writes about 8 MB into a scratch table and drops it, leaving the
// pages on the freelist the way the retention sweep does.
func fillAndFree(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE scratch (b BLOB)`); err != nil {
		t.Fatalf("create scratch: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2000)
		INSERT INTO scratch SELECT randomblob(4000) FROM n`); err != nil {
		t.Fatalf("fill scratch: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE scratch`); err != nil {
		t.Fatalf("drop scratch: %v", err)
	}
	CheckpointDB(ctx, s.db, 0, quietLogger())
}

func stats(t *testing.T, s *Store) (mode, pages, free int64) {
	t.Helper()
	mode, _, pages, free, err := pageStats(context.Background(), s.db)
	if err != nil {
		t.Fatalf("page stats: %v", err)
	}
	return mode, pages, free
}

func TestCompactShrinksFileAndSwitchesToIncremental(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := open(dbPath, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	fillAndFree(t, store)
	mode, pagesBefore, free := stats(t, store)
	if mode == autoVacuumIncremental {
		t.Fatal("a new database is expected in auto_vacuum=NONE; test cannot prove the switch")
	}
	if free < 1000 {
		t.Fatalf("freelist = %d pages, want the dropped table's pages on it", free)
	}

	// A reader pod keeps the file open read-only while the writer starts.
	reader, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer reader.Close()
	if _, err := reader.ProjectBySlug(context.Background(), "default"); err != nil {
		t.Fatalf("reader query: %v", err)
	}

	res, err := store.Compact(context.Background(), dbPath, 1<<30)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !res.Compacted {
		t.Fatalf("compact skipped: %s", res.Reason)
	}
	if _, err := reader.ProjectBySlug(context.Background(), "default"); err != nil {
		t.Errorf("reader query after compact: %v", err)
	}
	mode, pagesAfter, free := stats(t, store)
	if mode != autoVacuumIncremental {
		t.Errorf("auto_vacuum = %d after compact, want %d", mode, autoVacuumIncremental)
	}
	if free != 0 {
		t.Errorf("freelist = %d pages after compact, want 0", free)
	}
	if pagesAfter >= pagesBefore {
		t.Errorf("page count %d -> %d, want it to shrink", pagesBefore, pagesAfter)
	}
	fi, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() >= res.BytesBefore {
		t.Errorf("file is %d bytes after compact, was %d", fi.Size(), res.BytesBefore)
	}

	// Data survives the rewrite.
	if _, err := store.ProjectBySlug(context.Background(), "default"); err != nil {
		t.Errorf("default project after compact: %v", err)
	}

	// A second run is a no-op.
	again, err := store.Compact(context.Background(), dbPath, 1<<30)
	if err != nil {
		t.Fatalf("second compact: %v", err)
	}
	if again.Compacted {
		t.Error("second compact rewrote the database again")
	}
}

// After Compact, pages freed later go back to the filesystem through the
// checkpoint loop's incremental vacuum.
func TestIncrementalVacuumReturnsFreedPagesAfterCompact(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := open(dbPath, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if _, err := store.Compact(ctx, dbPath, 1<<30); err != nil {
		t.Fatalf("compact: %v", err)
	}
	fillAndFree(t, store)
	_, pagesBefore, free := stats(t, store)
	if free < 1000 {
		t.Fatalf("freelist = %d pages, want the dropped table's pages on it", free)
	}

	incrementalVacuum(ctx, store.db, checkpointVacuumPages, quietLogger())
	_, pagesAfter, freeAfter := stats(t, store)
	if freeAfter != 0 {
		t.Errorf("freelist = %d pages after incremental vacuum, want 0", freeAfter)
	}
	if pagesAfter >= pagesBefore {
		t.Errorf("page count %d -> %d, want it to shrink", pagesBefore, pagesAfter)
	}
}

func TestCompactSkipsWhenLiveDataExceedsLimit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := open(dbPath, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	res, err := store.Compact(context.Background(), dbPath, 1)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if res.Compacted || res.Reason == "" {
		t.Fatalf("compact = %+v, want a skip with a reason", res)
	}
	if mode, _, _ := stats(t, store); mode == autoVacuumIncremental {
		t.Error("a skipped compact switched auto_vacuum")
	}
}

func TestRemoveLitestreamShadow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bugbarn.db")
	wal := filepath.Join(dir, ".bugbarn.db-litestream", "generations", "abc", "wal")
	if err := os.MkdirAll(wal, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wal, "00000001.wal"), make([]byte, 1234), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := RemoveLitestreamShadow(dbPath)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed != 1234 {
		t.Errorf("removed = %d bytes, want 1234", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, ".bugbarn.db-litestream")); !os.IsNotExist(err) {
		t.Errorf("shadow directory still present: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database file touched: %v", err)
	}

	// Absent directory: nothing to do.
	if removed, err := RemoveLitestreamShadow(dbPath); err != nil || removed != 0 {
		t.Errorf("second remove = %d, %v; want 0, nil", removed, err)
	}
}
