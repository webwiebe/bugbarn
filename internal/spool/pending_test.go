package spool

import (
	"os"
	"testing"
)

func TestReadPendingStartsAtTheCursor(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer s.Close()
	if err := s.Append(Record{IngestID: "a"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(s.Path())
	handled := info.Size()
	if err := s.AppendBatch([]Record{{IngestID: "b"}, {IngestID: "c"}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteCursor(dir, handled); err != nil {
		t.Fatal(err)
	}

	got, err := ReadPending(dir)
	if err != nil || len(got) != 2 || got[0].Record.IngestID != "b" || got[1].Record.IngestID != "c" {
		t.Fatalf("pending = %+v, %v; want b and c", got, err)
	}
	end, _ := os.Stat(s.Path())
	if got[1].After.Segment != "" || got[1].After.Offset != end.Size() || got[0].Drained != "" {
		t.Errorf("positions = %+v", got)
	}
}

func TestReadPendingSpansARotatedSegmentThenTheActiveOne(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer s.Close()
	if err := s.AppendBatch([]Record{{IngestID: "a"}, {IngestID: "b"}}); err != nil {
		t.Fatal(err)
	}
	segment, err := s.RotateIfExceedsAt(1, 0)
	if err != nil || segment == "" {
		t.Fatalf("rotate = %q, %v", segment, err)
	}
	if err := s.Append(Record{IngestID: "c"}); err != nil {
		t.Fatal(err)
	}

	got, err := ReadPending(dir)
	if err != nil || len(got) != 3 {
		t.Fatalf("pending = %+v, %v; want 3", got, err)
	}
	if got[0].After.Segment != segment || got[0].Drained != "" {
		t.Errorf("first = %+v, want a position inside the rotated segment", got[0])
	}
	if got[1].Drained != segment || (got[1].After != Position{}) {
		t.Errorf("second = %+v, want it to finish the rotated segment", got[1])
	}
	if got[2].Record.IngestID != "c" || got[2].After.Segment != "" {
		t.Errorf("third = %+v, want the active record", got[2])
	}
}

// A cursor that names a segment that was never renamed resumes the active file.
func TestReadPendingTreatsAMissingSegmentAsTheActiveOne(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer s.Close()
	if err := s.AppendBatch([]Record{{IngestID: "a"}, {IngestID: "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := WritePosition(dir, Position{Segment: "ingest-never-rotated.ndjson", Offset: 0}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPending(dir)
	if err != nil || len(got) != 2 {
		t.Fatalf("pending = %+v, %v; want both active records", got, err)
	}
}
