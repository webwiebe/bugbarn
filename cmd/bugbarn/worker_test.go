package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

// newTestWorker builds a spoolWorker pointed at a temp spool dir with a live
// status tracker and self-reporting disabled (so no network calls).
func newTestWorker(t *testing.T) *spoolWorker {
	t.Helper()
	return &spoolWorker{
		spoolDir:    t.TempDir(),
		ws:          worker.NewStatus(),
		retryCounts: make(map[string]int),
	}
}

func TestFailRecord_DeadLettersAfterBudget(t *testing.T) {
	rec := spool.Record{IngestID: "ing-1"}
	w := newTestWorker(t)
	cause := errors.New("boom")

	// First two failures only accrue retries: no dead letter, cursor untouched.
	for i := 1; i <= workerMaxRetries-1; i++ {
		w.failRecord(rec, 128, "process record", cause, true)
		if w.retryCounts[rec.IngestID] != i {
			t.Fatalf("after failure %d: retryCounts=%d want %d", i, w.retryCounts[rec.IngestID], i)
		}
		if off, _ := spool.ReadCursor(w.spoolDir); off != 0 {
			t.Fatalf("cursor advanced too early: %d", off)
		}
	}

	// The final failure dead-letters the record and advances past it.
	w.failRecord(rec, 128, "process record", cause, true)
	if _, ok := w.retryCounts[rec.IngestID]; ok {
		t.Fatalf("retry count not cleared after dead-letter: %v", w.retryCounts)
	}
	if off, _ := spool.ReadCursor(w.spoolDir); off != 128 {
		t.Fatalf("cursor not advanced past dead-lettered record: got %d want 128", off)
	}
	if data, err := os.ReadFile(filepath.Join(w.spoolDir, "deadletter.ndjson")); err != nil || len(data) == 0 {
		t.Fatalf("expected dead-letter file with content, err=%v len=%d", err, len(data))
	}
	if snap := w.ws.Snapshot(); snap.DeadLetterCount != 1 {
		t.Fatalf("expected 1 dead-letter recorded, got %d", snap.DeadLetterCount)
	}
}

// TestFailRecord_NoReportSkipsMetric mirrors the release path, which dead-letters
// without touching the self-report / dead-letter metric.
func TestFailRecord_NoReportSkipsMetric(t *testing.T) {
	rec := spool.Record{IngestID: "rel-1"}
	w := newTestWorker(t)
	for i := 0; i < workerMaxRetries; i++ {
		w.failRecord(rec, 64, "persist release", errors.New("nope"), false)
	}
	if off, _ := spool.ReadCursor(w.spoolDir); off != 64 {
		t.Fatalf("cursor not advanced: got %d want 64", off)
	}
	if snap := w.ws.Snapshot(); snap.DeadLetterCount != 0 {
		t.Fatalf("release dead-letter should not increment the metric, got %d", snap.DeadLetterCount)
	}
}

func TestMarkProcessed_AdvancesCursor(t *testing.T) {
	rec := spool.Record{IngestID: "ing-2"}
	w := newTestWorker(t)
	w.retryCounts[rec.IngestID] = 2
	w.markProcessed(rec, 256)
	if _, ok := w.retryCounts[rec.IngestID]; ok {
		t.Fatal("markProcessed should clear retry count")
	}
	if off, _ := spool.ReadCursor(w.spoolDir); off != 256 {
		t.Fatalf("cursor not advanced: got %d want 256", off)
	}
	if snap := w.ws.Snapshot(); snap.ProcessedTotal != 1 {
		t.Fatalf("expected 1 processed, got %d", snap.ProcessedTotal)
	}
}

// A shutdown must not be charged to the record. The cursor is not advanced on
// failure, so the record is retried from the same offset after restart —
// spending retries on cancellations would eventually dead-letter a good
// record, and each one was logged at ERROR, which self-reported a bug against
// us on every deploy (BS2-105).
func TestFailRecord_ShutdownDoesNotConsumeRetries(t *testing.T) {
	rec := spool.Record{IngestID: "ing-shutdown"}

	for name, cause := range map[string]error{
		"canceled":          context.Canceled,
		"deadline exceeded": context.DeadlineExceeded,
		"wrapped canceled":  fmt.Errorf("persist release: %w", context.Canceled),
	} {
		t.Run(name, func(t *testing.T) {
			w := newTestWorker(t)

			// Far more failures than the retry budget: none may count.
			for range workerMaxRetries * 3 {
				w.failRecord(rec, 128, "persist release", cause, false)
			}

			if n, ok := w.retryCounts[rec.IngestID]; ok && n != 0 {
				t.Errorf("shutdown consumed %d retries, want 0", n)
			}
			if off, _ := spool.ReadCursor(w.spoolDir); off != 0 {
				t.Errorf("cursor advanced past a record interrupted by shutdown: %d", off)
			}
			if _, err := os.Stat(filepath.Join(w.spoolDir, "deadletter.ndjson")); err == nil {
				t.Error("a shutdown dead-lettered the record")
			}
			if snap := w.ws.Snapshot(); snap.DeadLetterCount != 0 {
				t.Errorf("dead-letter recorded for a shutdown: %d", snap.DeadLetterCount)
			}
		})
	}
}
