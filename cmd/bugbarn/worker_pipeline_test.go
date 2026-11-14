package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// pipelineWorker is newSegmentWorker with the project policy chosen by the test.
func pipelineWorker(t *testing.T, autoApprove bool) (*spoolWorker, *spool.Spool, *storage.Store) {
	t.Helper()
	w, sp, store := newSegmentWorker(t, t.TempDir())
	w.proc = ingestproc.NewProcessor(store, domainevents.NewIssuePublisher(&domainevents.Bus{}), nil, autoApprove)
	w.metrics = newWorkerMetrics()
	return w, sp, store
}

func slugged(t *testing.T, id, slug string) spool.Record {
	t.Helper()
	rec := goodRecord(t, id)
	rec.ProjectSlug = slug
	return rec
}

func cursorAtEnd(t *testing.T, w *spoolWorker) bool {
	t.Helper()
	return mustPosition(t, w.spoolDir).Offset == activeSize(t, w.spoolDir)
}

func deadLetterExists(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "deadletter.ndjson"))
	return err == nil && len(data) > 0
}

// logBuffer swaps the default slog logger for one that writes to a buffer.
// The logger is process-wide, so tests using it do not run in parallel.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureLogs(t *testing.T) *logBuffer {
	t.Helper()
	prev := slog.Default()
	lb := &logBuffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return lb
}

// An unknown project slug is held, not created active. This is the rule the
// queue consumer has always applied; the spool worker used to create the
// project active and persist the event.
func TestSpoolHoldsEventsForAnUnknownProject(t *testing.T) {
	w, sp, store := pipelineWorker(t, false)
	appendRecords(t, sp, slugged(t, "held-1", "brand-new"))

	w.tick(context.Background())

	ctx := context.Background()
	proj, err := store.ProjectBySlug(ctx, "brand-new")
	if err != nil {
		t.Fatalf("project not created: %v", err)
	}
	if proj.Status != "pending" {
		t.Errorf("project status = %q, want pending", proj.Status)
	}
	if n, _ := store.CountHeldByProject(ctx, proj.ID); n != 1 {
		t.Errorf("held count = %d, want 1", n)
	}
	if n := eventCount(t, store); n != 0 {
		t.Errorf("events stored = %d, want 0 while pending", n)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance past the held record")
	}
	if snap := w.ws.Snapshot(); snap.ProcessedTotal != 1 || snap.DeadLetterCount != 0 {
		t.Errorf("status = %+v, want 1 processed and no dead letter", snap)
	}
}

func TestSpoolPersistsForAnUnknownProjectUnderAutoApprove(t *testing.T) {
	w, sp, store := pipelineWorker(t, true)
	appendRecords(t, sp, slugged(t, "auto-1", "auto-new"))

	w.tick(context.Background())

	proj, err := store.ProjectBySlug(context.Background(), "auto-new")
	if err != nil {
		t.Fatalf("project not created: %v", err)
	}
	if proj.Status != "active" {
		t.Errorf("project status = %q, want active", proj.Status)
	}
	if n := eventCount(t, store); n != 1 {
		t.Errorf("events stored = %d, want 1", n)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance")
	}
}

// What the spool held replays through the same pipeline once the project is
// approved, exactly as for the queue consumer.
func TestSpoolHeldEventsReplayOnApproval(t *testing.T) {
	w, sp, store := pipelineWorker(t, false)
	appendRecords(t, sp, slugged(t, "r-1", "to-approve"), slugged(t, "r-2", "to-approve"))
	w.tick(context.Background())

	ctx := context.Background()
	proj, err := store.ProjectBySlug(ctx, "to-approve")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if err := store.ApproveProject(ctx, "to-approve"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	replayer := ingestproc.NewReplayer(store, w.proc, nil, nil)
	n, err := replayer.ReplayHeld(ctx, proj.ID)
	if err != nil || n != 2 {
		t.Fatalf("ReplayHeld = %d, %v; want 2, nil", n, err)
	}
	if got := eventCount(t, store); got != 2 {
		t.Errorf("events after replay = %d, want 2", got)
	}
}

func TestSpoolDeadLettersAnUnparseableRecordAfterTheBudget(t *testing.T) {
	w, sp, store := pipelineWorker(t, true)
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
	if n := eventCount(t, store); n != 1 {
		t.Errorf("events stored = %d, want the good record after the dead letter", n)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not reach the end of the spool")
	}
}

// A persist failure that is not lock contention counts toward the budget.
func TestSpoolDeadLettersAPermanentPersistFailure(t *testing.T) {
	w, sp, store := pipelineWorker(t, true)
	appendRecords(t, sp, goodRecord(t, "perm-1"))
	store.Close()
	ctx := context.Background()

	for range workerMaxRetries {
		w.tick(ctx)
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
func TestSpoolRetriesTransientFailuresWithoutSpendingTheBudget(t *testing.T) {
	w, sp, store := pipelineWorker(t, true)
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
	if n := eventCount(t, store); n != 1 {
		t.Errorf("events stored = %d after the condition cleared, want 1", n)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance once the record persisted")
	}
}

// ERROR means a record was lost. A failed attempt that will be retried logs at
// WARN, so BugBarn does not file a bug against itself for it.
func TestSpoolLogsRetriedFailuresAtWarnAndTheDeadLetterAtError(t *testing.T) {
	logs := captureLogs(t)
	w, sp, _ := pipelineWorker(t, true)
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

func releaseSpoolRecord(id, body string, projectID int64) spool.Record {
	return spool.Record{
		IngestID:   id,
		Kind:       ingest.RecordKindRelease,
		ProjectID:  projectID,
		BodyBase64: base64.StdEncoding.EncodeToString([]byte(body)),
	}
}

func TestSpoolPersistsReleaseMarkers(t *testing.T) {
	w, sp, store := pipelineWorker(t, true)
	ctx := context.Background()
	proj, err := store.EnsureProject(ctx, "rel-proj")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	appendRecords(t, sp, releaseSpoolRecord("rel-1", `{"name":"v9","environment":"prod"}`, proj.ID))

	w.tick(ctx)

	releases, err := store.ListReleases(domain.WithProjectID(ctx, proj.ID))
	if err != nil || len(releases) != 1 || releases[0].Name != "v9" {
		t.Fatalf("releases = %+v, %v; want one named v9", releases, err)
	}
	if !cursorAtEnd(t, w) {
		t.Error("cursor did not advance past the release")
	}
}

func TestSpoolDeadLettersABrokenReleaseWithoutSelfReporting(t *testing.T) {
	w, sp, _ := pipelineWorker(t, true)
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
	w, sp, store := pipelineWorker(t, true)
	ctx := context.Background()
	appendRecords(t, sp, goodRecord(t, "m-1"))
	w.tick(ctx)
	issues, err := store.ListIssues(ctx)
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues = %d, %v; want 1", len(issues), err)
	}
	id := issues[0].ID

	for _, op := range []mutqueue.Record{
		{IssueID: id, Op: mutqueue.OpResolve},
	} {
		if err := w.mq.Append(op); err != nil {
			t.Fatalf("queue mutation: %v", err)
		}
	}
	w.tick(ctx)
	got, err := store.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.Status != "resolved" {
		t.Errorf("status = %q, want resolved", got.Status)
	}
}

func TestApplyMutationCoversEveryOp(t *testing.T) {
	w, sp, store := pipelineWorker(t, true)
	ctx := context.Background()
	appendRecords(t, sp, goodRecord(t, "ap-1"))
	w.tick(ctx)
	issues, _ := store.ListIssues(ctx)
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %d", len(issues))
	}
	id := issues[0].ID

	status := func() string {
		got, err := store.GetIssue(ctx, id)
		if err != nil {
			t.Fatalf("get issue: %v", err)
		}
		return got.Status
	}
	steps := []struct {
		rec  mutqueue.Record
		want string
	}{
		{mutqueue.Record{IssueID: id, Op: mutqueue.OpResolve}, "resolved"},
		{mutqueue.Record{IssueID: id, Op: mutqueue.OpReopen}, "unresolved"},
		{mutqueue.Record{IssueID: id, Op: mutqueue.OpMute, MuteMode: "until_regression"}, "muted"},
		{mutqueue.Record{IssueID: id, Op: mutqueue.OpUnmute}, "unresolved"},
	}
	for _, step := range steps {
		if err := applyMutation(ctx, store, step.rec); err != nil {
			t.Fatalf("%s: %v", step.rec.Op, err)
		}
		if got := status(); got != step.want {
			t.Fatalf("after %s status = %q, want %q", step.rec.Op, got, step.want)
		}
	}

	logs := captureLogs(t)
	if err := applyMutation(ctx, store, mutqueue.Record{IssueID: id, Op: "bogus"}); err != nil {
		t.Errorf("unknown op must be skipped, got %v", err)
	}
	if !strings.Contains(logs.String(), "unknown op") {
		t.Errorf("unknown op not logged: %s", logs.String())
	}
}

func TestReplayRecordsSummarisesAMaintenancePass(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	proj, err := store.EnsureProject(ctx, "once-proj")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	proc := ingestproc.NewProcessor(store, domainevents.NewIssuePublisher(&domainevents.Bus{}), nil, false)

	records := []spool.Record{
		slugged(t, "o-1", "once-proj"),
		slugged(t, "o-2", "once-proj"),
		slugged(t, "o-3", "unapproved"),
		releaseSpoolRecord("o-4", `{"name":"v1"}`, proj.ID),
	}
	got, err := replayRecords(ctx, proc, records)
	if err != nil {
		t.Fatalf("replayRecords: %v", err)
	}
	want := replaySummary{Records: 4, Events: 2, Releases: 1, Held: 1, Issues: 1}
	if got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}

	if _, err := replayRecords(ctx, proc, []spool.Record{{IngestID: "x", BodyBase64: "!!!"}}); err == nil {
		t.Error("an unparseable record must stop the pass with its error")
	}
	if _, err := replayRecords(ctx, proc, []spool.Record{releaseSpoolRecord("r", "{", 0)}); err == nil {
		t.Error("a broken release must stop the pass with its error")
	}
}

func TestRunWorkerOnceReadsTheSpoolAndPrintsTheSummary(t *testing.T) {
	dir := t.TempDir()
	sp, err := spool.New(dir)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	appendRecords(t, sp, goodRecord(t, "w1-1"))
	sp.Close()

	r, wpipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = wpipe
	runErr := runWorkerOnce(testConfig(t, dir))
	os.Stdout = stdout
	wpipe.Close()
	if runErr != nil {
		t.Fatalf("runWorkerOnce: %v", runErr)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(out.String(), `"events":1`) || !strings.Contains(out.String(), `"records":1`) {
		t.Errorf("summary = %s", out.String())
	}
}

func TestRunBackgroundWorkerReturnsWhenTheContextIsDone(t *testing.T) {
	_, sp, store := pipelineWorker(t, true)
	dir := t.TempDir()
	mq, err := mutqueue.New(dir)
	if err != nil {
		t.Fatalf("mutqueue: %v", err)
	}
	defer mq.Close()
	proc := ingestproc.NewProcessor(store, domainevents.NewIssuePublisher(&domainevents.Bus{}), nil, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runBackgroundWorker(ctx, sp, dir, store, proc, false, nil, mq)
	}()
	<-done
}

// testConfig points a maintenance command at a temp database and the given spool.
func testConfig(t *testing.T, spoolDir string) config.Config {
	t.Helper()
	return config.Config{
		SpoolDir:            spoolDir,
		DBPath:              filepath.Join(t.TempDir(), "bugbarn.db"),
		AutoApproveProjects: true,
	}
}
