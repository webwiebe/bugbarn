package spool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSegmentName(t *testing.T) {
	cases := map[string]bool{
		"ingest-20260629T025734.685489947Z.ndjson":     true,
		"ingest-20260629T025734.685489947Z-001.ndjson": true,
		DefaultFileName:      false,
		cursorFileName:       false,
		cursorTmpName:        false,
		deadLetterFileName:   false,
		"mutations.ndjson":   false,
		"ingest-.ndjson":     false,
		"ingest-../x.ndjson": false,
	}
	for name, want := range cases {
		if got := isSegmentName(name); got != want {
			t.Errorf("isSegmentName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRemoveSegmentRefusesNonSegments(t *testing.T) {
	dir := t.TempDir()
	sp, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	for _, name := range []string{DefaultFileName, cursorFileName, deadLetterFileName} {
		if _, err := RemoveSegment(dir, name); err == nil {
			t.Errorf("RemoveSegment(%q) succeeded", name)
		}
	}
	if _, err := os.Stat(Path(dir)); err != nil {
		t.Fatalf("active segment gone: %v", err)
	}
}

func TestPositionRoundTripAndLegacyCursor(t *testing.T) {
	dir := t.TempDir()

	// A cursor written before segments were tracked reads as the active segment.
	if err := os.WriteFile(filepath.Join(dir, cursorFileName), []byte(`{"offset":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadPosition(dir); err != nil || got != (Position{Offset: 42}) {
		t.Fatalf("legacy cursor = %+v, %v", got, err)
	}

	want := Position{Segment: "ingest-20261006T120000.000000000Z.ndjson", Offset: 7}
	if err := CommitPosition(dir, want); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadPosition(dir); err != nil || got != want {
		t.Fatalf("committed position = %+v, %v; want %+v", got, err, want)
	}
	if off, err := ReadCursor(dir); err != nil || off != 7 {
		t.Fatalf("ReadCursor = %d, %v", off, err)
	}
	if err := WriteCursor(dir, 9); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadPosition(dir); err != nil || got != (Position{Offset: 9}) {
		t.Fatalf("after WriteCursor position = %+v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, cursorTmpName)); !os.IsNotExist(err) {
		t.Fatalf("temporary cursor file left behind: %v", err)
	}
}

// RotateIfExceedsAt commits a cursor naming the new segment at the caller's
// offset, so records behind the cursor at rotation time are still read.
func TestRotateIfExceedsAtCommitsCursor(t *testing.T) {
	dir := t.TempDir()
	sp, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	for _, id := range []string{"a", "b"} {
		if err := sp.Append(Record{IngestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := ReadRecordsFrom(Path(dir), 0)
	if err != nil || len(recs) != 2 {
		t.Fatalf("read: %v (%d)", err, len(recs))
	}

	// Below the threshold nothing happens.
	if name, err := sp.RotateIfExceedsAt(recs[1].EndOffset, recs[0].EndOffset); err != nil || name != "" {
		t.Fatalf("rotation below threshold: name=%q err=%v", name, err)
	}

	name, err := sp.RotateIfExceedsAt(1, recs[0].EndOffset)
	if err != nil || name == "" {
		t.Fatalf("rotate: name=%q err=%v", name, err)
	}
	pos, err := ReadPosition(dir)
	if err != nil || pos != (Position{Segment: name, Offset: recs[0].EndOffset}) {
		t.Fatalf("cursor after rotation = %+v, %v", pos, err)
	}
	rest, err := ReadRecordsFrom(SegmentPath(dir, pos.Segment), pos.Offset)
	if err != nil || len(rest) != 1 || rest[0].Record.IngestID != "b" {
		t.Fatalf("records left in rotated segment = %+v, %v", rest, err)
	}
	segs, err := ListSegments(dir)
	if err != nil || len(segs) != 1 || segs[0] != name {
		t.Fatalf("segments = %v, %v", segs, err)
	}
	if err := sp.Append(Record{IngestID: "c"}); err != nil {
		t.Fatal(err)
	}
	active, err := ReadRecordsFrom(Path(dir), 0)
	if err != nil || len(active) != 1 || active[0].Record.IngestID != "c" {
		t.Fatalf("active segment after rotation = %+v, %v", active, err)
	}
}
