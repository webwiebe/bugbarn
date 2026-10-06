package spool

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadBacklogEmptySpool(t *testing.T) {
	dir := t.TempDir()
	b, err := ReadBacklog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Bytes != 0 || !b.LastAdvanceAt.IsZero() {
		t.Fatalf("empty spool: got %+v", b)
	}
}

func TestReadBacklogActiveSegment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, Path(dir), 1000)
	if err := WritePosition(dir, Position{Offset: 400}); err != nil {
		t.Fatal(err)
	}
	b, err := ReadBacklog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Bytes != 600 {
		t.Fatalf("backlog: got %d, want 600", b.Bytes)
	}
	if b.LastAdvanceAt.IsZero() {
		t.Fatal("expected the cursor's modification time")
	}
}

func TestReadBacklogDrainingRotatedSegment(t *testing.T) {
	dir := t.TempDir()
	segment := "ingest-20261006T180000.000000000Z.ndjson"
	writeFile(t, filepath.Join(dir, segment), 5000)
	writeFile(t, Path(dir), 300)
	if err := WritePosition(dir, Position{Segment: segment, Offset: 4000}); err != nil {
		t.Fatal(err)
	}
	b, err := ReadBacklog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Bytes != 1300 {
		t.Fatalf("backlog: got %d, want 1000 left in the segment plus 300 active", b.Bytes)
	}
}

// A sample can land between the worker deleting a drained segment and the
// reader seeing the new cursor, or right after a rotation while the cursor
// still holds the old active offset. Neither may read as negative.
func TestReadBacklogClampsRaces(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, Path(dir), 100)
	if err := WritePosition(dir, Position{Offset: 900}); err != nil {
		t.Fatal(err)
	}
	b, err := ReadBacklog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Bytes != 0 {
		t.Fatalf("cursor past the end of the active segment: got %d, want 0", b.Bytes)
	}

	if err := WritePosition(dir, Position{Segment: "ingest-20261006T180000.000000000Z.ndjson", Offset: 900}); err != nil {
		t.Fatal(err)
	}
	b, err = ReadBacklog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Bytes != 100 {
		t.Fatalf("deleted segment: got %d, want only the active segment's 100", b.Bytes)
	}
}

func TestReadBacklogMissingDir(t *testing.T) {
	if _, err := ReadBacklog(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing spool directory")
	}
}
