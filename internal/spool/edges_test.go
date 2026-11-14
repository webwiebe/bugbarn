package spool

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNilSpoolFailsSafely(t *testing.T) {
	var sp *Spool
	if sp.Path() != "" {
		t.Error("nil spool has a path")
	}
	if err := sp.Append(Record{}); err == nil {
		t.Error("Append on a nil spool must fail")
	}
	if err := sp.AppendBatch([]Record{{}}); err == nil {
		t.Error("AppendBatch on a nil spool must fail")
	}
	if _, err := sp.RotateIfExceedsAt(1, 0); err == nil {
		t.Error("RotateIfExceedsAt on a nil spool must fail")
	}
	if err := sp.Close(); err != nil {
		t.Errorf("Close on a nil spool: %v", err)
	}
}

func TestSpoolPathReportsTheActiveSegment(t *testing.T) {
	dir := t.TempDir()
	sp, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	if got, want := sp.Path(), filepath.Join(dir, DefaultFileName); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestNewFailsWhenTheDirectoryCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(filepath.Join(blocker, "spool")); err == nil {
		t.Fatal("New under a regular file must fail")
	}
}

func TestEmptyDirFallsBackToTheDefaultLocation(t *testing.T) {
	t.Chdir(t.TempDir())

	sp, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	if err := sp.Append(Record{IngestID: "a"}); err != nil {
		t.Fatal(err)
	}
	if got := Path(""); got != filepath.Join(".data/spool", DefaultFileName) {
		t.Fatalf("Path(\"\") = %q", got)
	}
	if got := SegmentPath("", ""); got != Path("") {
		t.Fatalf("SegmentPath active = %q", got)
	}
	if got := SegmentPath("", "ingest-x.ndjson"); got != filepath.Join(".data/spool", "ingest-x.ndjson") {
		t.Fatalf("SegmentPath rotated = %q", got)
	}

	if err := WriteCursor("", 42); err != nil {
		t.Fatal(err)
	}
	if off, err := ReadCursor(""); err != nil || off != 42 {
		t.Fatalf("ReadCursor = %d, %v", off, err)
	}
	if pos, err := ReadPosition(""); err != nil || pos.Offset != 42 {
		t.Fatalf("ReadPosition = %+v, %v", pos, err)
	}
	if err := CommitPosition("", Position{Offset: 7}); err != nil {
		t.Fatal(err)
	}
	if err := AppendDeadLetter("", Record{IngestID: "dead"}); err != nil {
		t.Fatal(err)
	}
	if segs, err := ListSegments(""); err != nil || len(segs) != 0 {
		t.Fatalf("ListSegments = %v, %v", segs, err)
	}
	if b, err := ReadBacklog(""); err != nil || b.Bytes <= 0 {
		t.Fatalf("ReadBacklog = %+v, %v", b, err)
	}
	if err := ResetCursor(""); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptCursorIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cursorFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCursor(dir); err == nil {
		t.Error("ReadCursor accepted a corrupt cursor")
	}
	if _, err := ReadPosition(dir); err == nil {
		t.Error("ReadPosition accepted a corrupt cursor")
	}
	if _, err := ReadBacklog(dir); err == nil || !strings.Contains(err.Error(), "read cursor") {
		t.Errorf("ReadBacklog error = %v, want a read cursor error", err)
	}
}

func TestUnreadableCursorIsAnError(t *testing.T) {
	dir := t.TempDir()
	// A directory where the cursor file belongs reads as an error, not as "missing".
	if err := os.Mkdir(filepath.Join(dir, cursorFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCursor(dir); err == nil {
		t.Error("ReadCursor must fail when cursor.json is a directory")
	}
	if _, err := ReadPosition(dir); err == nil {
		t.Error("ReadPosition must fail when cursor.json is a directory")
	}
}

func TestWritePositionFailsWithoutADirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if err := WritePosition(missing, Position{Offset: 1}); err == nil {
		t.Error("WritePosition into a missing dir must fail")
	}
	if err := CommitPosition(missing, Position{Offset: 1}); err == nil {
		t.Error("CommitPosition into a missing dir must fail")
	}
	if err := syncDir(missing); err == nil {
		t.Error("syncDir on a missing dir must fail")
	}
}

func TestWritePositionFailsWhenTheCursorPathIsADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, cursorFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WritePosition(dir, Position{Offset: 1}); err == nil {
		t.Error("renaming over a directory must fail")
	}
}

func TestResetCursorFailsOnANonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, cursorFileName)
	if err := os.MkdirAll(filepath.Join(cur, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ResetCursor(dir); err == nil {
		t.Error("ResetCursor must report a removal failure other than not-exist")
	}
}

func TestReadRecordsFailsOnACorruptLineButReadRecordsFromSkipsIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.ndjson")
	body := `{"ingestId":"a","bodyBase64":""}` + "\n" + "{broken\n" + "\n" + `{"ingestId":"b","bodyBase64":""}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecords(path); err == nil {
		t.Error("ReadRecords must fail on a corrupt line")
	}
	got, err := ReadRecordsFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Record.IngestID != "a" || got[1].Record.IngestID != "b" {
		t.Fatalf("records = %+v", got)
	}
	if int64(len(body)) != got[1].EndOffset {
		t.Errorf("last EndOffset = %d, want %d", got[1].EndOffset, len(body))
	}
}

func TestReadRecordsMissingFileIsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.ndjson")
	if recs, err := ReadRecords(missing); err != nil || recs != nil {
		t.Errorf("ReadRecords = %v, %v", recs, err)
	}
	if recs, err := ReadRecordsFrom(missing, 0); err != nil || recs != nil {
		t.Errorf("ReadRecordsFrom = %v, %v", recs, err)
	}
}

func TestReadRecordsFailsOnADirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadRecords(dir); err == nil {
		t.Error("ReadRecords on a directory must fail")
	}
	if _, err := ReadRecordsFrom(dir, 0); err == nil {
		t.Error("ReadRecordsFrom on a directory must fail")
	}
}

func TestAppendDeadLetterFailsWithoutADirectory(t *testing.T) {
	if err := AppendDeadLetter(filepath.Join(t.TempDir(), "missing"), Record{}); err == nil {
		t.Error("AppendDeadLetter into a missing dir must fail")
	}
}

func TestAppendBatchReturnsErrFullAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	sp, err := NewWithLimit(dir, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	big := []Record{{IngestID: strings.Repeat("a", 100)}}
	if err := sp.AppendBatch(big); !errors.Is(err, ErrFull) {
		t.Fatalf("AppendBatch = %v, want ErrFull", err)
	}
	if recs, err := ReadRecords(Path(dir)); err != nil || len(recs) != 0 {
		t.Fatalf("records after a rejected batch = %v, %v", recs, err)
	}
	if err := sp.AppendBatch(nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
}

func TestAppendAfterCloseFails(t *testing.T) {
	sp, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Append(Record{IngestID: "late"}); err == nil {
		t.Error("Append on a closed spool must fail")
	}
	if err := sp.AppendBatch([]Record{{IngestID: "late"}}); err == nil {
		t.Error("AppendBatch on a closed spool must fail")
	}
	limited, err := NewWithLimit(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := limited.Close(); err != nil {
		t.Fatal(err)
	}
	if err := limited.Append(Record{}); err == nil {
		t.Error("a limited spool must report a Stat failure after close")
	}
	if err := limited.AppendBatch([]Record{{}}); err == nil {
		t.Error("a limited spool must report a Stat failure after close (batch)")
	}
	if _, err := limited.RotateIfExceedsAt(1, 0); err == nil {
		t.Error("RotateIfExceedsAt must report a Stat failure after close")
	}
}

func TestSegmentListingAndExistenceErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := ListSegments(missing); err == nil {
		t.Error("ListSegments on a missing dir must fail")
	}

	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A path that runs through a regular file is neither present nor absent.
	if _, err := SegmentExists(blocker, "ingest-x.ndjson"); err == nil {
		t.Error("SegmentExists must report a stat error that is not not-exist")
	}
	if ok, err := SegmentExists(dir, "ingest-gone.ndjson"); err != nil || ok {
		t.Errorf("SegmentExists missing = %v, %v", ok, err)
	}
	if _, err := RemoveSegment(dir, "ingest-gone.ndjson"); err == nil {
		t.Error("RemoveSegment of a missing segment must fail")
	}
}

func TestListSegmentsIgnoresDirectoriesAndNonSegments(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "ingest-dir.ndjson"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ingest-b.ndjson", "ingest-a.ndjson", "other.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "ingest-a.ndjson,ingest-b.ndjson" {
		t.Fatalf("segments = %v", got)
	}
}

func TestReadBacklogRejectsAFileAsDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBacklog(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("ReadBacklog on a file = %v", err)
	}
}

func TestFileSizeReportsStatErrors(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fileSize(filepath.Join(blocker, "child")); err == nil {
		t.Fatal("fileSize must report a stat error other than not-exist")
	}
}

func TestSeekToOffsetOnAClosedFileFails(t *testing.T) {
	f, err := os.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := seekToOffset(f, 5); err == nil {
		t.Fatal("seekToOffset on a closed file must fail")
	}
}

func TestRotationAvoidsAnExistingSegmentName(t *testing.T) {
	dir := t.TempDir()
	sp, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		if err := sp.Append(Record{IngestID: "r"}); err != nil {
			t.Fatal(err)
		}
		name, err := sp.RotateIfExceedsAt(1, 0)
		if err != nil || name == "" {
			t.Fatalf("rotation %d: %q, %v", i, name, err)
		}
		if seen[name] {
			t.Fatalf("segment name %q reused", name)
		}
		seen[name] = true
	}
	segs, err := ListSegments(dir)
	if err != nil || len(segs) != 3 {
		t.Fatalf("segments = %v, %v", segs, err)
	}
}
