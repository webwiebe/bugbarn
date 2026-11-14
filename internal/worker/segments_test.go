package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// Rotation, processing and deletion: records in a rotated segment and in the
// new active segment are each processed once, and the rotated segment is gone
// once it is drained.
func TestWorkerDeletesRotatedSegmentOnceDrained(t *testing.T) {
	dir := t.TempDir()
	w, sp, fp := newSegmentWorker(t, dir)
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
	if got, want := fp.count(), int64(5); got != want {
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
	w, sp, fp := newSegmentWorker(t, dir)
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
	if got, want := fp.count(), int64(3); got != want {
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

	w, _, fp := newSegmentWorker(t, dir)
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
	if got := fp.count(); got != 1 {
		t.Fatalf("events persisted = %d, want 1 (only the unprocessed record)", got)
	}
}

// Restart after the cursor was committed for a rotation whose rename never
// happened: the worker resumes the active segment at the same offset.
func TestWorkerRestartAfterInterruptedRotation(t *testing.T) {
	dir := t.TempDir()
	w, sp, fp := newSegmentWorker(t, dir)
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
	if got := fp.count(); got != 1 {
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
	w, _, fp := newSegmentWorker(t, dir)
	w.restorePosition()
	if got := segments(t, dir); len(got) != 0 {
		t.Fatalf("leftover segment not deleted: %v", got)
	}
	w.tick(context.Background())
	if got := fp.count(); got != 0 {
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
	w, sp, fp := newSegmentWorker(t, dir)
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
	if got := fp.count(); got != 5 {
		t.Fatalf("events persisted = %d, want 5", got)
	}
}

func TestSpoolWorkerRotatesAndFinishesASegment(t *testing.T) {
	dir := t.TempDir()
	w, sp, _ := newSegmentWorker(t, dir)
	w.rotateThreshold = 1
	appendRecords(t, sp, goodRecord(t, "r1"))

	w.rotate()
	if w.pos.Segment == "" {
		t.Fatal("the active segment was not rotated")
	}
	if got := w.pendingRecords(); got != 1 {
		t.Fatalf("pending after rotation = %d, want 1", got)
	}
	w.finishSegment()
	if w.pos.Segment != "" || len(segments(t, dir)) != 0 {
		t.Fatalf("segment survived finishSegment: pos=%+v segments=%v", w.pos, segments(t, dir))
	}
}

func TestSpoolWorkerRotateWithoutASpoolIsANoOp(t *testing.T) {
	w := &spoolWorker{}
	w.rotate()
	if w.pos.Segment != "" {
		t.Fatalf("rotated without a spool: %+v", w.pos)
	}
}

func TestSpoolWorkerRotateKeepsPositionWhenTheSpoolIsClosed(t *testing.T) {
	logs := captureLogs(t)
	w, sp, _ := newSegmentWorker(t, t.TempDir())
	w.rotateThreshold = 1
	appendRecords(t, sp, goodRecord(t, "r1"))
	sp.Close()
	w.rotate()
	if w.pos.Segment != "" {
		t.Fatalf("position moved after a failed rotation: %+v", w.pos)
	}
	if !strings.Contains(logs.String(), "worker failed to rotate spool") {
		t.Fatalf("rotation failure not logged: %s", logs.String())
	}
}

func TestSpoolWorkerFinishSegmentStaysPutWhenTheCursorCannotBeCommitted(t *testing.T) {
	logs := captureLogs(t)
	dir := filepath.Join(t.TempDir(), "gone")
	w := &spoolWorker{spoolDir: dir, pos: spool.Position{Segment: "seg-1", Offset: 5}}
	w.finishSegment()
	if w.pos.Segment != "seg-1" {
		t.Fatalf("position moved without a durable cursor: %+v", w.pos)
	}
	if !strings.Contains(logs.String(), "failed to commit cursor") {
		t.Fatalf("commit failure not logged: %s", logs.String())
	}
}

func TestSpoolWorkerReportStatusWarnsOnceWhileStalledAndResetsWhenHealthy(t *testing.T) {
	logs := captureLogs(t)
	w, sp, _ := newSegmentWorker(t, t.TempDir())
	appendRecords(t, sp, goodRecord(t, "r1"))
	// A zero Status has never advanced, which is how a stalled worker looks.
	w.ws = &Status{}

	w.reportStatus()
	if !w.stallWarned {
		t.Fatal("a worker with pending records and no advance did not warn")
	}
	w.reportStatus()
	if n := strings.Count(logs.String(), "worker stall detected"); n != 1 {
		t.Fatalf("stall warned %d times, want once", n)
	}

	w.selfReporting = true
	w.stallWarned = false
	w.reportStatus()
	if !w.stallWarned {
		t.Fatal("the self-reporting stall path did not mark the warning")
	}

	empty, _, _ := newSegmentWorker(t, t.TempDir())
	empty.stallWarned = true
	empty.reportStatus()
	if empty.stallWarned {
		t.Fatal("a drained worker kept its stall warning")
	}
	(&spoolWorker{}).reportStatus()
}
