package api

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCursor must replace cursor.json atomically. A truncate-then-write leaves
// an empty file if the process dies in between, and the drain loop then fails
// to parse it on every pass (BS2-152). A hard link to the old file keeps its
// content only when the write goes to a new file that is renamed into place.
func TestWriteCursorReplacesFileInsteadOfTruncating(t *testing.T) {
	dir := t.TempDir()
	if err := writeCursor(dir, 5); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old-link")
	if err := os.Link(filepath.Join(dir, spoolCursorFileName), old); err != nil {
		t.Fatal(err)
	}

	if err := writeCursor(dir, 9); err != nil {
		t.Fatal(err)
	}

	if got, err := readCursor(dir); err != nil || got != 9 {
		t.Fatalf("readCursor = %d, %v; want 9, nil", got, err)
	}
	prev, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if string(prev) != `{"offset":5}` {
		t.Fatalf("old cursor file was rewritten in place: %q", prev)
	}
}

func TestWriteCursorLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	for i := int64(1); i <= 3; i++ {
		if err := writeCursor(dir, i); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != spoolCursorFileName {
		t.Fatalf("dir entries = %v; want only %s", entries, spoolCursorFileName)
	}
}
