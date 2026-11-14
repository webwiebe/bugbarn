package spool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendBatchWritesEveryRecordAndHonoursTheLimit(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer s.Close()
	if s.Path() != Path(dir) {
		t.Errorf("Path = %q, want %q", s.Path(), Path(dir))
	}
	if err := s.AppendBatch(nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	batch := []Record{{IngestID: "a"}, {IngestID: "b"}, {IngestID: "c"}}
	if err := s.AppendBatch(batch); err != nil {
		t.Fatalf("batch: %v", err)
	}
	got, err := ReadRecords(s.Path())
	if err != nil || len(got) != 3 || got[0].IngestID != "a" || got[2].IngestID != "c" {
		t.Fatalf("records = %+v, err %v", got, err)
	}

	small, err := NewWithLimit(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("new limited: %v", err)
	}
	defer small.Close()
	if err := small.AppendBatch(batch); !errors.Is(err, ErrFull) {
		t.Errorf("over the limit = %v, want ErrFull", err)
	}

	var nilSpool *Spool
	if nilSpool.Path() != "" || nilSpool.AppendBatch(batch) == nil {
		t.Error("a nil spool has no path and refuses a batch")
	}
}

func TestResetCursorRemovesTheCursorAndToleratesNone(t *testing.T) {
	dir := t.TempDir()
	if err := ResetCursor(dir); err != nil {
		t.Fatalf("reset without a cursor: %v", err)
	}
	if err := WriteCursor(dir, 42); err != nil {
		t.Fatalf("write cursor: %v", err)
	}
	if err := ResetCursor(dir); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if off, err := ReadCursor(dir); err != nil || off != 0 {
		t.Errorf("cursor after reset = %d, %v", off, err)
	}
}

func TestRemoveSegmentDeletesOnlyRotatedSegments(t *testing.T) {
	dir := t.TempDir()
	name := segmentPrefix + "20260101T000000" + segmentSuffix
	if err := os.WriteFile(SegmentPath(dir, name), []byte("12345"), 0o600); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	active := filepath.Join(dir, DefaultFileName)
	if err := os.WriteFile(active, []byte("x"), 0o600); err != nil {
		t.Fatalf("write active: %v", err)
	}

	if _, err := RemoveSegment(dir, DefaultFileName); err == nil {
		t.Error("the active segment must be refused")
	}
	if _, err := RemoveSegment(dir, "../"+name); err == nil {
		t.Error("a path outside the directory must be refused")
	}
	if _, err := RemoveSegment(dir, segmentPrefix+"missing"+segmentSuffix); err == nil {
		t.Error("a missing segment must report an error")
	}
	size, err := RemoveSegment(dir, name)
	if err != nil || size != 5 {
		t.Fatalf("remove = %d, %v, want 5 bytes", size, err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Errorf("active segment disturbed: %v", err)
	}
}
