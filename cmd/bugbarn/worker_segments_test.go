package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/service"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

const segmentFixturePath = "../../specs/001-personal-error-tracker/fixtures/example-event.json"

// newSegmentWorker builds a worker over a real spool and a real SQLite store in
// a temp dir, the way runBackgroundWorker wires them in the writer.
func newSegmentWorker(t *testing.T, dir string) (*spoolWorker, *spool.Spool, *storage.Store) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	sp, err := spool.New(dir)
	if err != nil {
		t.Fatalf("open spool: %v", err)
	}
	t.Cleanup(func() { sp.Close() })
	mq, err := mutqueue.New(dir)
	if err != nil {
		t.Fatalf("open mutation queue: %v", err)
	}
	t.Cleanup(func() { mq.Close() })
	w := &spoolWorker{
		eventSpool:  sp,
		spoolDir:    dir,
		store:       store,
		svc:         service.NewEventPublisher(&domainevents.Bus{}),
		ws:          worker.NewStatus(),
		mq:          mq,
		tracer:      tracing.Tracer(),
		retryCounts: make(map[string]int),
		// Keep rotation out of the way unless a test asks for it.
		rotateThreshold: 1 << 40,
	}
	return w, sp, store
}

func goodRecord(t *testing.T, id string) spool.Record {
	t.Helper()
	raw, err := os.ReadFile(segmentFixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return spool.Record{IngestID: id, ReceivedAt: time.Now().UTC(), BodyBase64: base64.StdEncoding.EncodeToString(raw)}
}

func appendRecords(t *testing.T, sp *spool.Spool, recs ...spool.Record) {
	t.Helper()
	for _, r := range recs {
		if err := sp.Append(r); err != nil {
			t.Fatalf("append %s: %v", r.IngestID, err)
		}
	}
}

func segments(t *testing.T, dir string) []string {
	t.Helper()
	names, err := spool.ListSegments(dir)
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	return names
}

func eventCount(t *testing.T, store *storage.Store) int64 {
	t.Helper()
	n, err := store.CountEventsBefore(context.Background(), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func mustPosition(t *testing.T, dir string) spool.Position {
	t.Helper()
	pos, err := spool.ReadPosition(dir)
	if err != nil {
		t.Fatalf("read position: %v", err)
	}
	return pos
}

func activeSize(t *testing.T, dir string) int64 {
	t.Helper()
	info, err := os.Stat(spool.Path(dir))
	if err != nil {
		t.Fatalf("stat active segment: %v", err)
	}
	return info.Size()
}

// Rotation, processing and deletion: records in a rotated segment and in the
// new active segment are each processed once, and the rotated segment is gone
// once it is drained.
func TestWorkerDeletesRotatedSegmentOnceDrained(t *testing.T) {
	dir := t.TempDir()
	w, sp, store := newSegmentWorker(t, dir)
	ctx := context.Background()

	appendRecords(t, sp, goodRecord(t, "a"), goodRecord(t, "b"), goodRecord(t, "c"))
	w.rotateThreshold = 1
	w.tick(ctx) // processes a, b, c and rotates the full active segment

	segs := segments(t, dir)
	if len(segs) != 1 {
		t.Fatalf("expected one rotated segment after the first tick, got %v", segs)
	}
	if w.pos.Segment != segs[0] {
		t.Fatalf("worker position %+v does not name the rotated segment %s", w.pos, segs[0])
	}
	if got := mustPosition(t, dir); got.Segment != segs[0] {
		t.Fatalf("persisted cursor %+v does not name the rotated segment %s", got, segs[0])
	}

	w.rotateThreshold = 1 << 40
	appendRecords(t, sp, goodRecord(t, "d"), goodRecord(t, "e"))
	w.tick(ctx) // segment drained: cursor moves to the active segment, segment deleted
	if segs := segments(t, dir); len(segs) != 0 {
		t.Fatalf("drained segment not deleted: %v", segs)
	}
	if w.pos != (spool.Position{}) {
		t.Fatalf("expected position at the start of the active segment, got %+v", w.pos)
	}

	w.tick(ctx) // processes d, e from the active segment
	if got, want := eventCount(t, store), int64(5); got != want {
		t.Fatalf("events persisted = %d, want %d", got, want)
	}
	if got, want := mustPosition(t, dir), (spool.Position{Offset: activeSize(t, dir)}); got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
}

// A rotated segment with an unprocessed record stays on disk until that record
// has been handled, and a record behind the cursor at rotation time is still
// processed from the rotated segment.
func TestWorkerKeepsSegmentWithUnprocessedRecords(t *testing.T) {
	dir := t.TempDir()
	w, sp, store := newSegmentWorker(t, dir)
	ctx := context.Background()

	bad := spool.Record{IngestID: "bad", BodyBase64: "not-base64-$$$"}
	appendRecords(t, sp, goodRecord(t, "a"), bad, goodRecord(t, "c"))

	// Rotate before anything is processed: all three records are in the segment.
	w.rotateThreshold = 1
	w.rotate()
	w.rotateThreshold = 1 << 40
	segs := segments(t, dir)
	if len(segs) != 1 || w.pos != (spool.Position{Segment: segs[0]}) {
		t.Fatalf("expected position at the start of the rotated segment, got %+v (segments %v)", w.pos, segs)
	}
	appendRecords(t, sp, goodRecord(t, "d"))

	// The bad record fails twice, is dead-lettered on the third attempt, and
	// stops the batch each time: the segment is not drained yet.
	for i := 1; i <= workerMaxRetries; i++ {
		w.tick(ctx)
		if got := segments(t, dir); len(got) != 1 {
			t.Fatalf("tick %d: segment with unprocessed records was deleted (segments %v)", i, got)
		}
		if w.pos.Segment != segs[0] {
			t.Fatalf("tick %d: cursor left the segment early: %+v", i, w.pos)
		}
	}

	w.tick(ctx) // processes c, the segment is drained and deleted
	if got := segments(t, dir); len(got) != 0 {
		t.Fatalf("drained segment not deleted: %v", got)
	}
	w.tick(ctx) // processes d from the active segment
	if got, want := eventCount(t, store), int64(3); got != want {
		t.Fatalf("events persisted = %d, want %d (a, c, d)", got, want)
	}
}

// The active segment, the cursor and the dead-letter file are never deleted.
func TestWorkerNeverDeletesActiveSegment(t *testing.T) {
	dir := t.TempDir()
	w, sp, _ := newSegmentWorker(t, dir)

	appendRecords(t, sp, goodRecord(t, "a"))
	if err := spool.WriteCursor(dir, 0); err != nil {
		t.Fatal(err)
	}
	if err := spool.AppendDeadLetter(dir, spool.Record{IngestID: "dl"}); err != nil {
		t.Fatal(err)
	}
	w.removeDrainedSegments()
	for _, name := range []string{spool.DefaultFileName, "cursor.json", "deadletter.ndjson", "mutations.ndjson"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s removed: %v", name, err)
		}
	}
	if _, err := spool.RemoveSegment(dir, spool.DefaultFileName); err == nil {
		t.Fatal("RemoveSegment accepted the active segment")
	}
}

// writeSegment creates a rotated segment file holding the given records.
func writeSegment(t *testing.T, dir, name string, recs ...spool.Record) {
	t.Helper()
	tmp := t.TempDir()
	sp, err := spool.New(tmp)
	if err != nil {
		t.Fatal(err)
	}
	appendRecords(t, sp, recs...)
	sp.Close()
	if err := os.Rename(spool.Path(tmp), filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

// Restart while a rotated segment is being drained: the worker resumes the
// segment at the persisted offset, deletes segments left behind by older
// versions, and deletes the drained segment afterwards.
func TestWorkerRestartResumesRotatedSegment(t *testing.T) {
	dir := t.TempDir()
	const draining = "ingest-20261006T120000.000000000Z.ndjson"
	const legacy = "ingest-20260629T025734.685489947Z.ndjson"
	writeSegment(t, dir, legacy, goodRecord(t, "old"))
	writeSegment(t, dir, draining, goodRecord(t, "done"), goodRecord(t, "todo"))
	done, err := spool.ReadRecordsFrom(filepath.Join(dir, draining), 0)
	if err != nil || len(done) != 2 {
		t.Fatalf("read draining segment: %v (%d records)", err, len(done))
	}
	if err := spool.CommitPosition(dir, spool.Position{Segment: draining, Offset: done[0].EndOffset}); err != nil {
		t.Fatal(err)
	}

	w, _, store := newSegmentWorker(t, dir)
	w.restorePosition()
	if got := segments(t, dir); len(got) != 1 || got[0] != draining {
		t.Fatalf("after restart segments = %v, want only %s", got, draining)
	}
	if w.pos != (spool.Position{Segment: draining, Offset: done[0].EndOffset}) {
		t.Fatalf("restored position %+v", w.pos)
	}

	w.tick(context.Background())
	if got := segments(t, dir); len(got) != 0 {
		t.Fatalf("drained segment not deleted: %v", got)
	}
	if got := eventCount(t, store); got != 1 {
		t.Fatalf("events persisted = %d, want 1 (only the unprocessed record)", got)
	}
}

// Restart after the cursor was committed for a rotation whose rename never
// happened: the worker resumes the active segment at the same offset.
func TestWorkerRestartAfterInterruptedRotation(t *testing.T) {
	dir := t.TempDir()
	w, sp, store := newSegmentWorker(t, dir)
	appendRecords(t, sp, goodRecord(t, "done"), goodRecord(t, "todo"))
	recs, err := spool.ReadRecordsFrom(spool.Path(dir), 0)
	if err != nil || len(recs) != 2 {
		t.Fatalf("read active segment: %v (%d records)", err, len(recs))
	}
	missing := "ingest-20261006T130000.000000000Z.ndjson"
	if err := spool.CommitPosition(dir, spool.Position{Segment: missing, Offset: recs[0].EndOffset}); err != nil {
		t.Fatal(err)
	}

	w.restorePosition()
	if w.pos != (spool.Position{Offset: recs[0].EndOffset}) {
		t.Fatalf("restored position %+v, want the active segment at %d", w.pos, recs[0].EndOffset)
	}
	w.tick(context.Background())
	if got := eventCount(t, store); got != 1 {
		t.Fatalf("events persisted = %d, want 1", got)
	}
}

// Restart after the cursor moved off a drained segment but before the delete:
// the leftover segment is deleted at startup.
func TestWorkerRestartDeletesDrainedLeftover(t *testing.T) {
	dir := t.TempDir()
	leftover := "ingest-20261006T140000.000000000Z.ndjson"
	writeSegment(t, dir, leftover, goodRecord(t, "done"))
	if err := spool.CommitPosition(dir, spool.Position{}); err != nil {
		t.Fatal(err)
	}
	w, _, store := newSegmentWorker(t, dir)
	w.restorePosition()
	if got := segments(t, dir); len(got) != 0 {
		t.Fatalf("leftover segment not deleted: %v", got)
	}
	w.tick(context.Background())
	if got := eventCount(t, store); got != 0 {
		t.Fatalf("leftover segment was re-processed: %d events", got)
	}
}

// An unreadable cursor gives no way to tell which segment is still being
// drained, so nothing is deleted.
func TestWorkerRestartWithCorruptCursorKeepsSegments(t *testing.T) {
	dir := t.TempDir()
	seg := "ingest-20261006T150000.000000000Z.ndjson"
	writeSegment(t, dir, seg, goodRecord(t, "maybe-todo"))
	if err := os.WriteFile(filepath.Join(dir, "cursor.json"), []byte("{trunc"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _, _ := newSegmentWorker(t, dir)
	w.restorePosition()
	if got := segments(t, dir); len(got) != 1 {
		t.Fatalf("segments deleted despite an unreadable cursor: %v", got)
	}
}

// Many rotations in a row each leave exactly one segment that is drained and
// deleted before the next rotation, so the spool never accumulates segments.
func TestWorkerSpoolDoesNotAccumulateSegments(t *testing.T) {
	dir := t.TempDir()
	w, sp, store := newSegmentWorker(t, dir)
	w.rotateThreshold = 1
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		appendRecords(t, sp, goodRecord(t, fmt.Sprintf("r%d", i)))
		w.tick(ctx) // process + rotate
		w.tick(ctx) // drain + delete (+ rotate an empty active segment: no-op)
		if got := segments(t, dir); len(got) != 0 {
			t.Fatalf("round %d: segments left behind: %v", i, got)
		}
	}
	if got := eventCount(t, store); got != 5 {
		t.Fatalf("events persisted = %d, want 5", got)
	}
}
