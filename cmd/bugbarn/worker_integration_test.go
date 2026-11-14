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
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

const segmentFixturePath = "../../specs/001-personal-error-tracker/fixtures/example-event.json"

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

func slugged(t *testing.T, id, slug string) spool.Record {
	t.Helper()
	rec := goodRecord(t, id)
	rec.ProjectSlug = slug
	return rec
}

func releaseSpoolRecord(id, body string, projectID int64) spool.Record {
	return spool.Record{
		IngestID:   id,
		Kind:       ingest.RecordKindRelease,
		ProjectID:  projectID,
		BodyBase64: base64.StdEncoding.EncodeToString([]byte(body)),
	}
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

// waitFor polls a real condition (database state, cursor position) until it
// holds or the deadline passes. The worker ticks once a second.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
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

func spoolDrained(dir string) bool {
	pos, err := spool.ReadPosition(dir)
	if err != nil || pos.Segment != "" {
		return false
	}
	info, err := os.Stat(spool.Path(dir))
	return err == nil && info.Size() > 0 && pos.Offset == info.Size()
}

// The spool worker, adapted to the real ingestproc pipeline and store: an event
// for an approved project is stored, an event for an unknown project is held,
// a release marker creates the release, and a queued mutation is applied.
func TestRunBackgroundWorkerDrivesTheRealPipeline(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	sp, err := spool.New(dir)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	t.Cleanup(func() { sp.Close() })
	mq, err := mutqueue.New(dir)
	if err != nil {
		t.Fatalf("mutqueue: %v", err)
	}
	t.Cleanup(func() { mq.Close() })
	ctx := context.Background()
	active, err := store.EnsureProject(ctx, "known")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	proc := ingestproc.NewProcessor(store, domainevents.NewIssuePublisher(&domainevents.Bus{}), nil, false)
	appendRecords(t, sp,
		slugged(t, "known-1", "known"),
		slugged(t, "held-1", "brand-new"),
		releaseSpoolRecord("rel-1", `{"name":"v9","environment":"prod"}`, active.ID),
	)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runBackgroundWorker(runCtx, sp, dir, store, proc, false, worker.NewStatus(), mq)
	}()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "the spool to drain", func() bool { return spoolDrained(dir) })

	if proj, err := store.ProjectBySlug(ctx, "brand-new"); err != nil || proj.Status != "pending" {
		t.Fatalf("unknown project = %+v, %v; want pending", proj, err)
	}
	releases, err := store.ListReleases(domain.WithProjectID(ctx, active.ID))
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	sawMarker := false
	for _, rel := range releases {
		sawMarker = sawMarker || rel.Name == "v9"
	}
	if !sawMarker {
		t.Fatalf("releases = %+v; want the v9 marker", releases)
	}
	issues, err := store.ListIssues(domain.WithProjectID(ctx, active.ID))
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues = %d, %v; want 1", len(issues), err)
	}
	id := issues[0].ID

	if err := mq.Append(mutqueue.Record{IssueID: id, Op: mutqueue.OpResolve}); err != nil {
		t.Fatalf("queue mutation: %v", err)
	}
	waitFor(t, "the queued mutation", func() bool {
		got, err := store.GetIssue(domain.WithProjectID(ctx, active.ID), id)
		return err == nil && got.Status == "resolved"
	})
}

func TestWorkerOutcomeMapsEveryPipelineOutcome(t *testing.T) {
	for in, want := range map[ingestproc.Outcome]worker.PersistOutcome{
		ingestproc.OutcomeSuccess:      worker.OutcomeSuccess,
		ingestproc.OutcomeHeld:         worker.OutcomeHeld,
		ingestproc.OutcomeTransient:    worker.OutcomeTransient,
		ingestproc.OutcomeParseError:   worker.OutcomeParseError,
		ingestproc.OutcomePersistError: worker.OutcomePersistError,
	} {
		if got := workerOutcome(in); got != want {
			t.Errorf("workerOutcome(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestSpoolPersisterReportsTheFingerprintAndFlags(t *testing.T) {
	store := openTestStore(t)
	p := spoolPersister{proc: ingestproc.NewProcessor(store, domainevents.NewIssuePublisher(&domainevents.Bus{}), nil, true)}

	res := p.PersistRecord(context.Background(), goodRecord(t, "sp-1"))
	if res.Outcome != worker.OutcomeSuccess || res.Fingerprint == "" || !res.IsNew {
		t.Errorf("result = %+v, want a new success with a fingerprint", res)
	}
	bad := p.PersistRecord(context.Background(), spool.Record{IngestID: "sp-2", BodyBase64: "!!!"})
	if bad.Outcome != worker.OutcomeParseError || bad.Err == nil {
		t.Errorf("bad record = %+v, want a parse error", bad)
	}
	if err := p.PersistRelease(context.Background(), releaseSpoolRecord("sp-3", "{", 0)); err == nil {
		t.Error("a broken release was accepted")
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
	got, err := replayPending(ctx, proc, t.TempDir(), pendingOf(records))
	if err != nil {
		t.Fatalf("replayPending: %v", err)
	}
	want := replaySummary{Records: 4, Events: 2, Releases: 1, Held: 1, Issues: 1}
	if got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}

	if _, err := replayPending(ctx, proc, t.TempDir(), pendingOf([]spool.Record{{IngestID: "x", BodyBase64: "!!!"}})); err == nil {
		t.Error("an unparseable record must stop the pass with its error")
	}
	if _, err := replayPending(ctx, proc, t.TempDir(), pendingOf([]spool.Record{releaseSpoolRecord("r", "{", 0)})); err == nil {
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

// testConfig points a maintenance command at a temp database and the given spool.
func testConfig(t *testing.T, spoolDir string) config.Config {
	t.Helper()
	return config.Config{
		SpoolDir:            spoolDir,
		DBPath:              filepath.Join(t.TempDir(), "bugbarn.db"),
		AutoApproveProjects: true,
	}
}
