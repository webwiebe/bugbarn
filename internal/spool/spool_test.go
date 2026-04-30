package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAppendWritesNDJSON(t *testing.T) {
	dir := t.TempDir()

	eventSpool, err := New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer eventSpool.Close()

	record := Record{
		IngestID:    "abc123",
		ReceivedAt:  time.Unix(123, 0).UTC(),
		ContentType: "application/json",
		RemoteAddr:  "127.0.0.1:4321",
		BodyBase64:  "eyJmb28iOiJiYXIifQ==",
	}

	if err := eventSpool.Append(record); err != nil {
		t.Fatalf("append: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, DefaultFileName))
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}

	var got Record
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode record: %v", err)
	}

	if got.IngestID != record.IngestID {
		t.Fatalf("expected ingest id %q, got %q", record.IngestID, got.IngestID)
	}

	if got.BodyBase64 != record.BodyBase64 {
		t.Fatalf("expected body %q, got %q", record.BodyBase64, got.BodyBase64)
	}
}

func TestReadRecordsReturnsAppendedRecords(t *testing.T) {
	dir := t.TempDir()
	eventSpool, err := New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer eventSpool.Close()

	first := Record{IngestID: "first", BodyBase64: "e30="}
	second := Record{IngestID: "second", BodyBase64: "e30="}
	if err := eventSpool.Append(first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := eventSpool.Append(second); err != nil {
		t.Fatalf("append second: %v", err)
	}

	records, err := ReadRecords(Path(dir))
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("unexpected record count: got %d want %d", got, want)
	}
	if records[0].IngestID != "first" || records[1].IngestID != "second" {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestAppendReturnsErrFullWhenLimitWouldBeExceeded(t *testing.T) {
	eventSpool, err := NewWithLimit(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer eventSpool.Close()

	err = eventSpool.Append(Record{IngestID: "too-large", BodyBase64: "e30="})
	if !errors.Is(err, ErrFull) {
		t.Fatalf("expected ErrFull, got %v", err)
	}
}

func TestCursorPersistenceAndRecovery(t *testing.T) {
	dir := t.TempDir()

	sp, err := New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer sp.Close()

	first := Record{IngestID: "r1", BodyBase64: "e30="}
	second := Record{IngestID: "r2", BodyBase64: "e30="}
	third := Record{IngestID: "r3", BodyBase64: "e30="}
	for _, r := range []Record{first, second, third} {
		if err := sp.Append(r); err != nil {
			t.Fatalf("append %s: %v", r.IngestID, err)
		}
	}

	// Read all from offset 0.
	entries, err := ReadRecordsFrom(Path(dir), 0)
	if err != nil {
		t.Fatalf("read records from 0: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Simulate processing only the first two records and persisting cursor.
	cursorAfterTwo := entries[1].EndOffset
	if err := WriteCursor(dir, cursorAfterTwo); err != nil {
		t.Fatalf("write cursor: %v", err)
	}

	// Verify cursor is readable.
	got, err := ReadCursor(dir)
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if got != cursorAfterTwo {
		t.Fatalf("expected cursor %d, got %d", cursorAfterTwo, got)
	}

	// Recovery: read from saved cursor — should only return the third record.
	recovered, err := ReadRecordsFrom(Path(dir), got)
	if err != nil {
		t.Fatalf("read records from cursor: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("expected 1 record after recovery, got %d", len(recovered))
	}
	if recovered[0].Record.IngestID != "r3" {
		t.Fatalf("expected r3, got %s", recovered[0].Record.IngestID)
	}
}

func TestCursorZeroWhenMissing(t *testing.T) {
	dir := t.TempDir()
	offset, err := ReadCursor(dir)
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if offset != 0 {
		t.Fatalf("expected 0, got %d", offset)
	}
}

func TestDeadLetterAppend(t *testing.T) {
	dir := t.TempDir()

	record := Record{IngestID: "bad-record", BodyBase64: "e30="}
	if err := AppendDeadLetter(dir, record); err != nil {
		t.Fatalf("append dead letter: %v", err)
	}

	dlPath := filepath.Join(dir, deadLetterFileName)
	raw, err := os.ReadFile(dlPath)
	if err != nil {
		t.Fatalf("read dead letter file: %v", err)
	}

	var got Record
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode dead letter record: %v", err)
	}
	if got.IngestID != "bad-record" {
		t.Fatalf("expected ingest id bad-record, got %s", got.IngestID)
	}
}

func TestDeadLetterAppendsMultiple(t *testing.T) {
	dir := t.TempDir()

	for _, id := range []string{"dl-1", "dl-2", "dl-3"} {
		if err := AppendDeadLetter(dir, Record{IngestID: id, BodyBase64: "e30="}); err != nil {
			t.Fatalf("append dead letter %s: %v", id, err)
		}
	}

	dlPath := filepath.Join(dir, deadLetterFileName)
	records, err := ReadRecords(dlPath)
	if err != nil {
		t.Fatalf("read dead letter records: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 dead-letter records, got %d", len(records))
	}
}

func TestRotate(t *testing.T) {
	dir := t.TempDir()

	sp, err := New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer sp.Close()

	if err := sp.Append(Record{IngestID: "before-rotate", BodyBase64: "e30="}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if err := sp.Rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Active spool file should be fresh (empty).
	info, err := os.Stat(filepath.Join(dir, DefaultFileName))
	if err != nil {
		t.Fatalf("stat active spool: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected empty active spool after rotate, got %d bytes", info.Size())
	}

	// Archived segment should exist with the old content.
	entries, err := filepath.Glob(filepath.Join(dir, "ingest-*.ndjson"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 archived segment, got %d", len(entries))
	}
}

func TestReadRecordsFromOffsets(t *testing.T) {
	dir := t.TempDir()
	sp, err := New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer sp.Close()

	ids := []string{"a", "b", "c"}
	for _, id := range ids {
		if err := sp.Append(Record{IngestID: id, BodyBase64: "e30="}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}

	all, err := ReadRecordsFrom(Path(dir), 0)
	if err != nil {
		t.Fatalf("read from 0: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}

	// Reading from the end offset of the second record should yield only the third.
	tail, err := ReadRecordsFrom(Path(dir), all[1].EndOffset)
	if err != nil {
		t.Fatalf("read from offset: %v", err)
	}
	if len(tail) != 1 || tail[0].Record.IngestID != "c" {
		t.Fatalf("expected [c], got %+v", tail)
	}
}

// TestRotateIfExceedsConcurrentAppend verifies that every record appended
// concurrently with RotateIfExceeds ends up in exactly one segment and is
// not silently discarded.
func TestRotateIfExceedsConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer s.Close()

	const writers = 8
	const recordsPerWriter = 20
	total := writers * recordsPerWriter

	var wg sync.WaitGroup

	// Hammer RotateIfExceeds from one goroutine while writers append concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			// Threshold of 1 byte forces rotation on almost every call.
			if err := s.RotateIfExceeds(1); err != nil {
				t.Errorf("RotateIfExceeds: %v", err)
				return
			}
		}
	}()

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < recordsPerWriter; i++ {
				rec := Record{IngestID: fmt.Sprintf("w%d-r%d", w, i)}
				if err := s.Append(rec); err != nil {
					t.Errorf("append w%d r%d: %v", w, i, err)
				}
			}
		}(w)
	}

	wg.Wait()

	// Collect every record across all segments (active + archived).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	seen := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".ndjson" {
			continue
		}
		if e.Name() == cursorFileName || e.Name() == deadLetterFileName {
			continue
		}
		recs, err := ReadRecords(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, r := range recs {
			if seen[r.IngestID] {
				t.Errorf("duplicate record %q", r.IngestID)
			}
			seen[r.IngestID] = true
		}
	}
	if len(seen) != total {
		t.Errorf("expected %d records across all segments, got %d", total, len(seen))
	}
}
