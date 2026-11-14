package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// A held outcome is a handled record: the cursor moves past it and the status
// counts it as processed, not as a dead letter.
func TestSpoolWorkerAdvancesPastAHeldRecord(t *testing.T) {
	w, sp, fp := newSegmentWorker(t, t.TempDir())
	fp.heldSlugs["brand-new"] = true
	appendRecords(t, sp, slugged(t, "held-1", "brand-new"))

	w.tick(context.Background())

	if n := fp.count(); n != 0 {
		t.Errorf("events persisted = %d, want 0 while the project is pending", n)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance past the held record")
	}
	if snap := w.ws.Snapshot(); snap.ProcessedTotal != 1 || snap.DeadLetterCount != 0 {
		t.Errorf("status = %+v, want 1 processed and no dead letter", snap)
	}
}

func TestSpoolWorkerPersistsAGoodRecordAndAdvances(t *testing.T) {
	w, sp, fp := newSegmentWorker(t, t.TempDir())
	appendRecords(t, sp, slugged(t, "auto-1", "auto-new"))

	w.tick(context.Background())

	if n := fp.count(); n != 1 {
		t.Errorf("events persisted = %d, want 1", n)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance")
	}
}

func TestSpoolWorkerDeadLettersAnUnparseableRecordAfterTheBudget(t *testing.T) {
	w, sp, fp := newSegmentWorker(t, t.TempDir())
	bad := spool.Record{IngestID: "bad-1", ReceivedAt: time.Now().UTC(), BodyBase64: "!!!"}
	appendRecords(t, sp, bad, goodRecord(t, "good-after"))
	ctx := context.Background()

	for i := 1; i < workerMaxRetries; i++ {
		w.tick(ctx)
		if w.retryCounts["bad-1"] != i {
			t.Fatalf("after tick %d: retry count = %d, want %d", i, w.retryCounts["bad-1"], i)
		}
		if mustPosition(t, w.spoolDir).Offset != 0 || deadLetterExists(w.spoolDir) {
			t.Fatalf("tick %d moved the cursor or dead-lettered early", i)
		}
	}
	w.tick(ctx) // the final attempt dead-letters and moves on
	if !deadLetterExists(w.spoolDir) {
		t.Fatal("record not dead-lettered after the retry budget")
	}
	if snap := w.ws.Snapshot(); snap.DeadLetterCount != 1 {
		t.Errorf("dead letters = %d, want 1", snap.DeadLetterCount)
	}
	w.tick(ctx) // the next record is no longer blocked
	if n := fp.count(); n != 1 {
		t.Errorf("events persisted = %d, want the good record after the dead letter", n)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not reach the end of the spool")
	}
}

// A persist failure that is not lock contention counts toward the budget.
func TestSpoolWorkerDeadLettersAPermanentPersistFailure(t *testing.T) {
	w, sp, fp := newSegmentWorker(t, t.TempDir())
	fp.failPersist = true
	appendRecords(t, sp, goodRecord(t, "perm-1"))

	for range workerMaxRetries {
		w.tick(context.Background())
	}
	if !deadLetterExists(w.spoolDir) {
		t.Fatal("record not dead-lettered after repeated persist failures")
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not move past the dead letter")
	}
}

// Lock contention and deadlines never spend the budget: the record stays at the
// cursor and persists once the condition clears.
func TestSpoolWorkerRetriesTransientFailuresWithoutSpendingTheBudget(t *testing.T) {
	w, sp, fp := newSegmentWorker(t, t.TempDir())
	appendRecords(t, sp, goodRecord(t, "slow-1"))

	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	for range workerMaxRetries * 3 {
		w.tick(expired)
	}
	if len(w.retryCounts) != 0 {
		t.Errorf("transient failures counted: %v", w.retryCounts)
	}
	if deadLetterExists(w.spoolDir) || w.ws.Snapshot().DeadLetterCount != 0 {
		t.Error("transient failure dead-lettered the record")
	}
	if mustPosition(t, w.spoolDir).Offset != 0 {
		t.Error("cursor moved past a record that was never stored")
	}

	w.tick(context.Background())
	if n := fp.count(); n != 1 {
		t.Errorf("events persisted = %d after the condition cleared, want 1", n)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance once the record persisted")
	}
}

// ERROR means a record was lost. A failed attempt that will be retried logs at
// WARN, so BugBarn does not file a bug against itself for it.
func TestSpoolWorkerLogsRetriedFailuresAtWarnAndTheDeadLetterAtError(t *testing.T) {
	logs := captureLogs(t)
	w, sp, _ := newSegmentWorker(t, t.TempDir())
	appendRecords(t, sp, spool.Record{IngestID: "lv-1", ReceivedAt: time.Now().UTC(), BodyBase64: "!!!"})

	w.tick(context.Background())
	if out := logs.String(); !strings.Contains(out, "level=WARN") || strings.Contains(out, "level=ERROR") {
		t.Fatalf("first failed attempt must log WARN only, got:\n%s", out)
	}
	for range workerMaxRetries - 1 {
		w.tick(context.Background())
	}
	out := logs.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "dead-lettering record") {
		t.Errorf("the dead letter must log at ERROR, got:\n%s", out)
	}
	if strings.Count(out, "level=ERROR") != 1 {
		t.Errorf("want exactly one ERROR line for the lost record, got:\n%s", out)
	}
}

func TestSpoolWorkerPersistsReleaseMarkers(t *testing.T) {
	w, sp, fp := newSegmentWorker(t, t.TempDir())
	appendRecords(t, sp, releaseSpoolRecord("rel-1", `{"name":"v9","environment":"prod"}`, 7))

	w.tick(context.Background())

	if len(fp.releases) != 1 || fp.releases[0] != "rel-1" {
		t.Fatalf("releases = %v, want rel-1", fp.releases)
	}
	if fp.count() != 0 {
		t.Error("a release marker went through the event path")
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance past the release")
	}
}

func TestSpoolWorkerDeadLettersABrokenReleaseWithoutSelfReporting(t *testing.T) {
	w, sp, _ := newSegmentWorker(t, t.TempDir())
	appendRecords(t, sp, releaseSpoolRecord("rel-bad", "{not json", 0))
	for range workerMaxRetries {
		w.tick(context.Background())
	}
	if !deadLetterExists(w.spoolDir) {
		t.Fatal("broken release not dead-lettered")
	}
	if snap := w.ws.Snapshot(); snap.DeadLetterCount != 0 {
		t.Errorf("release dead letters are not counted, got %d", snap.DeadLetterCount)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance past the dead letter")
	}
}

func TestTickAppliesQueuedMutationsBeforeEvents(t *testing.T) {
	w, sp, fp := newSegmentWorker(t, t.TempDir())
	mut := &fakeMutator{}
	w.store = mut
	appendRecords(t, sp, goodRecord(t, "m-1"))
	if err := w.mq.Append(mutqueue.Record{IssueID: "iss-1", Op: mutqueue.OpResolve}); err != nil {
		t.Fatalf("queue mutation: %v", err)
	}

	w.tick(context.Background())

	if len(mut.calls) != 1 || mut.calls[0] != "resolve:iss-1" {
		t.Errorf("mutations applied = %v, want resolve:iss-1", mut.calls)
	}
	if fp.count() != 1 {
		t.Errorf("events persisted = %d, want 1", fp.count())
	}
}

func TestApplyMutationCoversEveryOp(t *testing.T) {
	ctx := context.Background()
	mut := &fakeMutator{}
	for _, rec := range []mutqueue.Record{
		{IssueID: "i", Op: mutqueue.OpResolve},
		{IssueID: "i", Op: mutqueue.OpReopen},
		{IssueID: "i", Op: mutqueue.OpMute, MuteMode: "until_regression"},
		{IssueID: "i", Op: mutqueue.OpUnmute},
	} {
		if err := applyMutation(ctx, mut, rec); err != nil {
			t.Fatalf("%s: %v", rec.Op, err)
		}
	}
	want := []string{"resolve:i", "reopen:i", "mute(until_regression):i", "unmute:i"}
	if strings.Join(mut.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", mut.calls, want)
	}

	logs := captureLogs(t)
	if err := applyMutation(ctx, mut, mutqueue.Record{IssueID: "i", Op: "bogus"}); err != nil {
		t.Errorf("unknown op must be skipped, got %v", err)
	}
	if !strings.Contains(logs.String(), "unknown op") {
		t.Errorf("unknown op not logged: %s", logs.String())
	}
	if len(mut.calls) != len(want) {
		t.Errorf("unknown op reached the store: %v", mut.calls)
	}
}

func TestRunSpoolWorkerReturnsWhenTheContextIsDone(t *testing.T) {
	dir := t.TempDir()
	_, sp, fp := newSegmentWorker(t, dir)
	mq, err := mutqueue.New(t.TempDir())
	if err != nil {
		t.Fatalf("mutqueue: %v", err)
	}
	defer mq.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunSpoolWorker(ctx, SpoolWorkerConfig{
			EventSpool: sp, SpoolDir: dir, Store: &fakeMutator{}, Persister: fp, Mutations: mq,
		})
	}()
	<-done
}
