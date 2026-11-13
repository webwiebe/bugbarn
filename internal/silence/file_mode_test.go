package silence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

type recordingFiler struct {
	fired   []Finding
	resumed []string
}

func (r *recordingFiler) Fire(_ context.Context, f Finding) error {
	r.fired = append(r.fired, f)
	return nil
}

func (r *recordingFiler) Resume(_ context.Context, slug, rule string) error {
	r.resumed = append(r.resumed, slug+"/"+rule)
	return nil
}

type recordingShipper struct{ shipped []Finding }

func (r *recordingShipper) Ship(_ context.Context, f Finding, _ Mode) {
	r.shipped = append(r.shipped, f)
}

func (f *fixture) rules(mode Mode, filer Filer, shipper Shipper) *Rules {
	cfg := DefaultRulesConfig()
	cfg.Mode = mode
	r := NewRules(cfg, StoreSource{ProjectStore: f.store.Domains().Projects}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return r.WithFiler(filer).WithShipper(shipper)
}

// eventWithFingerprint stores one event whose issue has the given fingerprint,
// as monitorwatch, detect and the silence reporter do.
func (f *fixture) eventWithFingerprint(projectID int64, fp string, at time.Time) {
	f.t.Helper()
	ctx := domain.WithProjectID(context.Background(), projectID)
	evt := event.Event{
		ReceivedAt: at, ObservedAt: at, Severity: "ERROR", Message: "self filed",
		Exception: event.Exception{Type: "monitor", Message: "self filed"},
	}
	if _, _, _, _, err := f.store.PersistProcessedEvent(ctx, worker.ProcessedEvent{
		Event: evt, Fingerprint: fp, FingerprintMaterial: fp,
	}); err != nil {
		f.t.Fatal(err)
	}
}

// A log stop files through the filer in file mode, and the same stream reporting
// again resolves it. Dry run ships the line and never calls the filer.
func TestFileModeFilesAStopAndResolvesItWhenLogsResume(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("github-tamagotchi")
	stop := now.Add(-(55*time.Hour + 54*time.Minute))
	f.logs(p.ID, every(stop.Add(-5*24*time.Hour), stop, time.Hour/70))

	filer, shipper := &recordingFiler{}, &recordingShipper{}
	f.rules(ModeFile, filer, shipper).Check(context.Background(), now)
	if len(filer.fired) != 1 || filer.fired[0].Slug != "github-tamagotchi" || filer.fired[0].Rule != RuleLogs {
		t.Fatalf("fired = %+v, want the log rule of github-tamagotchi", filer.fired)
	}
	if len(shipper.shipped) != 1 {
		t.Fatalf("shipped %d lines, want 1", len(shipper.shipped))
	}
	if len(filer.resumed) != 0 {
		t.Fatalf("resumed = %v before any new log row", filer.resumed)
	}

	f.logs(p.ID, []time.Time{now.Add(-time.Hour)})
	f.rules(ModeFile, filer, shipper).Check(context.Background(), now)
	if len(filer.fired) != 1 {
		t.Fatalf("fired again after the logs resumed: %+v", filer.fired)
	}
	if len(filer.resumed) != 1 || filer.resumed[0] != "github-tamagotchi/logs" {
		t.Fatalf("resumed = %v, want github-tamagotchi/logs", filer.resumed)
	}
}

func TestDryRunShipsTheLineAndNeverCallsTheFiler(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("github-tamagotchi")
	stop := now.Add(-(55*time.Hour + 54*time.Minute))
	f.logs(p.ID, every(stop.Add(-5*24*time.Hour), stop, time.Hour/70))
	f.logs(p.ID, []time.Time{now.Add(-time.Hour)}) // resumed: Resume must not be called either

	filer, shipper := &recordingFiler{}, &recordingShipper{}
	f.rules(ModeDryRun, filer, shipper).Check(context.Background(), now)
	if len(filer.fired)+len(filer.resumed) != 0 {
		t.Fatalf("dry run called the filer: fired %v resumed %v", filer.fired, filer.resumed)
	}

	// A stopped project in dry run is shipped.
	q := f.project("stopped")
	f.logs(q.ID, every(stop.Add(-5*24*time.Hour), stop, time.Hour/70))
	f.rules(ModeDryRun, filer, shipper).Check(context.Background(), now)
	if len(shipper.shipped) != 1 || shipper.shipped[0].Slug != "stopped" {
		t.Fatalf("shipped = %+v, want the stopped project", shipper.shipped)
	}
}

// A project that opted out is not judged at all.
func TestSilenceWatchOffSkipsTheProject(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("shut-down-on-purpose")
	stop := now.Add(-(55*time.Hour + 54*time.Minute))
	f.logs(p.ID, every(stop.Add(-5*24*time.Hour), stop, time.Hour/70))

	ctx := context.Background()
	if err := f.store.Domains().Projects.UpdateProjectSilenceWatch(ctx, p.Slug, storage.SilenceWatchOff); err != nil {
		t.Fatal(err)
	}
	if got := f.rules(ModeFile, &recordingFiler{}, nil).Check(ctx, now); len(got) != 0 {
		t.Fatalf("an opted-out project fired: %+v", got)
	}
	if err := f.store.Domains().Projects.UpdateProjectSilenceWatch(ctx, p.Slug, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.rules(ModeFile, &recordingFiler{}, nil).Check(ctx, now); len(got) != 1 {
		t.Fatalf("a watched project fired %d times, want 1", len(got))
	}
}

// Events BugBarn files into a project itself (monitor misses, detections,
// silence findings) are not activity of the app. A project whose app stopped
// 10 days ago still fires when a monitor-missed issue is newer.
func TestSelfFiledEventsDoNotHideAStop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("professionals")
	last := now.Add(-10 * 24 * time.Hour)
	var times []time.Time
	for d := 0; d < 6; d++ {
		times = append(times, last.Add(-time.Duration(d)*24*time.Hour))
	}
	f.events(p.ID, times)
	for _, fp := range []string{"monitor:nightly:missed", "detect:ssh-burst", "silence:professionals:events"} {
		f.eventWithFingerprint(p.ID, fp, now.Add(-time.Hour))
	}

	got := findingFor(f.check(), "professionals")
	if len(got) != 1 || got[0].Rule != RuleEvents {
		t.Fatalf("findings = %+v, want the event rule despite the self-filed events", got)
	}
	if got[0].Gap < 9*24*time.Hour || got[0].Gap > 11*24*time.Hour {
		t.Fatalf("gap = %s, want about 10 days (the app's last event)", got[0].Gap)
	}
}

type fakeIssues struct {
	target   storage.Project
	targetEr error
	issue    domain.Issue
	issueErr error
	resolved []string
	order    *[]string
}

func (f *fakeIssues) ProjectBySlug(context.Context, string) (storage.Project, error) {
	return f.target, f.targetEr
}

func (f *fakeIssues) IssueByFingerprint(context.Context, int64, string) (domain.Issue, error) {
	return f.issue, f.issueErr
}

func (f *fakeIssues) ResolveIssue(_ context.Context, id string) (domain.Issue, error) {
	f.resolved = append(f.resolved, id)
	if f.order != nil {
		*f.order = append(*f.order, "resolve")
	}
	return domain.Issue{}, nil
}

type persisted struct {
	records []spool.Record
	order   *[]string
}

func (p *persisted) persist(_ context.Context, rec spool.Record) error {
	p.records = append(p.records, rec)
	if p.order != nil {
		*p.order = append(*p.order, "event")
	}
	return nil
}

func decodeBody(t *testing.T, rec spool.Record) map[string]any {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(rec.BodyBase64)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func newTestReporter(store *fakeIssues, p *persisted) *Reporter {
	r := NewReporter(store, p.persist, "infra", slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.now = func() time.Time { return now }
	return r
}

func TestReporterFilesOneEventPerNewSilentProject(t *testing.T) {
	t.Parallel()
	store := &fakeIssues{target: storage.Project{ID: 9, Slug: "infra"}, issueErr: apperr.NotFound("issue not found", nil)}
	p := &persisted{}
	finding := Finding{Slug: "github-tamagotchi", Rule: RuleLogs, Gap: 56 * time.Hour, LongestGap: 30 * time.Minute, Volume: 5000}
	if err := newTestReporter(store, p).Fire(context.Background(), finding); err != nil {
		t.Fatal(err)
	}
	if len(p.records) != 1 {
		t.Fatalf("persisted %d events, want 1", len(p.records))
	}
	rec := p.records[0]
	if rec.ProjectSlug != "infra" || !rec.Internal {
		t.Errorf("record project %q internal %v, want infra and internal", rec.ProjectSlug, rec.Internal)
	}
	body := decodeBody(t, rec)
	if body["fingerprint"] != "silence:github-tamagotchi:logs" {
		t.Errorf("fingerprint = %v", body["fingerprint"])
	}
	if body["body"] != "Project silent: github-tamagotchi (logs)" || body["severityText"] != "error" {
		t.Errorf("body %v severity %v", body["body"], body["severityText"])
	}
}

func TestReporterLeavesAnOpenOrMutedIssueAlone(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"unresolved", "regressed", "muted"} {
		store := &fakeIssues{target: storage.Project{ID: 9}, issue: domain.Issue{ID: "1", Status: status}}
		p := &persisted{}
		if err := newTestReporter(store, p).Fire(context.Background(), Finding{Slug: "a", Rule: RuleEvents}); err != nil {
			t.Fatal(err)
		}
		if len(p.records) != 0 {
			t.Errorf("status %s: filed %d events on an existing issue, want 0", status, len(p.records))
		}
	}
}

// A resolved issue gets a new event, which regresses it.
func TestReporterReopensAResolvedIssue(t *testing.T) {
	t.Parallel()
	store := &fakeIssues{target: storage.Project{ID: 9}, issue: domain.Issue{ID: "1", Status: "resolved"}}
	p := &persisted{}
	if err := newTestReporter(store, p).Fire(context.Background(), Finding{Slug: "a", Rule: RuleEvents}); err != nil {
		t.Fatal(err)
	}
	if len(p.records) != 1 {
		t.Fatalf("filed %d events, want 1", len(p.records))
	}
}

func TestReporterResumeRecordsTheEventBeforeItResolves(t *testing.T) {
	t.Parallel()
	var order []string
	store := &fakeIssues{target: storage.Project{ID: 9}, issue: domain.Issue{ID: "42", Status: "unresolved"}, order: &order}
	p := &persisted{order: &order}
	if err := newTestReporter(store, p).Resume(context.Background(), "a", RuleLogs); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "event" || order[1] != "resolve" {
		t.Fatalf("order = %v, want event then resolve", order)
	}
	if len(store.resolved) != 1 || store.resolved[0] != "42" {
		t.Fatalf("resolved = %v, want 42", store.resolved)
	}
	if body := decodeBody(t, p.records[0]); body["severityText"] != "info" {
		t.Errorf("recovery severity = %v, want info", body["severityText"])
	}
}

func TestReporterResumeWithoutAnOpenIssueDoesNothing(t *testing.T) {
	t.Parallel()
	for name, store := range map[string]*fakeIssues{
		"no issue": {target: storage.Project{ID: 9}, issueErr: apperr.NotFound("issue not found", nil)},
		"resolved": {target: storage.Project{ID: 9}, issue: domain.Issue{ID: "1", Status: "resolved"}},
		"muted":    {target: storage.Project{ID: 9}, issue: domain.Issue{ID: "1", Status: "muted"}},
	} {
		p := &persisted{}
		if err := newTestReporter(store, p).Resume(context.Background(), "a", RuleLogs); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(p.records) != 0 || len(store.resolved) != 0 {
			t.Errorf("%s: filed %d events and resolved %v", name, len(p.records), store.resolved)
		}
	}
}

// A missing target project is an error the rules log at warn level; nothing is
// filed into another project.
func TestReporterWithoutATargetProjectFilesNothing(t *testing.T) {
	t.Parallel()
	store := &fakeIssues{targetEr: apperr.NotFound("project not found", nil)}
	p := &persisted{}
	err := newTestReporter(store, p).Fire(context.Background(), Finding{Slug: "a", Rule: RuleLogs})
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	if len(p.records) != 0 {
		t.Fatalf("filed %d events without a target project", len(p.records))
	}
}

type fakeLogs struct{ entries []storage.LogEntry }

func (f *fakeLogs) InsertLogEntries(_ context.Context, entries []storage.LogEntry) error {
	f.entries = append(f.entries, entries...)
	return nil
}

func TestLogShipperWritesAnInfoEntryIntoTheSelfProject(t *testing.T) {
	t.Parallel()
	logs := &fakeLogs{}
	s := NewLogShipper(logs, &fakeIssues{target: storage.Project{ID: 5, Slug: "bugbarn-service"}}, "bugbarn-service",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return now }
	s.Ship(context.Background(), Finding{Slug: "github-tamagotchi", Rule: RuleLogs, Gap: 56 * time.Hour, Volume: 5000}, ModeDryRun)
	if len(logs.entries) != 1 {
		t.Fatalf("stored %d entries, want 1", len(logs.entries))
	}
	e := logs.entries[0]
	if e.ProjectID != 5 || e.Level != "info" || e.LevelNum != 30 || e.Message != "silence rule would fire" {
		t.Errorf("entry = %+v", e)
	}
	if e.Data["project"] != "github-tamagotchi" || e.Data["rule"] != RuleLogs || e.Data["mode"] != "dry-run" {
		t.Errorf("data = %+v", e.Data)
	}
}

func TestLogShipperWithoutAProjectStoresNothing(t *testing.T) {
	t.Parallel()
	logs := &fakeLogs{}
	NewLogShipper(logs, &fakeIssues{}, "", slog.New(slog.NewTextHandler(io.Discard, nil))).
		Ship(context.Background(), Finding{Slug: "a", Rule: RuleLogs}, ModeDryRun)
	if len(logs.entries) != 0 {
		t.Fatalf("stored %d entries with no self project", len(logs.entries))
	}
}

func TestParseModeFile(t *testing.T) {
	t.Parallel()
	cases := map[string]Mode{"": ModeDryRun, "dry-run": ModeDryRun, "file": ModeFile, "off": ModeOff, "files": ModeOff, "FILE": ModeOff}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
}

type sweepShipper struct {
	recordingShipper
	sweeps [][3]int
}

func (s *sweepShipper) ShipSweep(_ context.Context, _ Mode, projects, wouldFire int) {
	s.sweeps = append(s.sweeps, [3]int{projects, wouldFire})
}

// A sweep that fires nothing still reaches the shipper, with the counts.
func TestEverySweepIsShippedWithItsCounts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.project("quiet-but-young")
	shipper := &sweepShipper{}
	f.rules(ModeDryRun, &recordingFiler{}, shipper).Check(context.Background(), now)
	if len(shipper.sweeps) != 1 || shipper.sweeps[0][0] < 1 || shipper.sweeps[0][1] != 0 {
		t.Fatalf("sweeps = %v, want one sweep of at least 1 project with 0 firing", shipper.sweeps)
	}
	if len(shipper.shipped) != 0 {
		t.Fatalf("shipped %d would-fire lines for a project with no history", len(shipper.shipped))
	}
}

func TestLogShipperWritesTheSweepCountsWithoutAProjectName(t *testing.T) {
	t.Parallel()
	logs := &fakeLogs{}
	s := NewLogShipper(logs, &fakeIssues{target: storage.Project{ID: 5, Slug: "bugbarn-service"}}, "bugbarn-service",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return now }
	s.ShipSweep(context.Background(), ModeDryRun, 55, 2)
	if len(logs.entries) != 1 {
		t.Fatalf("stored %d entries, want 1", len(logs.entries))
	}
	e := logs.entries[0]
	if e.ProjectID != 5 || e.Message != "silence rules swept" || e.Data["projects"] != 55 || e.Data["would_fire"] != 2 {
		t.Errorf("entry = %+v", e)
	}
	if _, ok := e.Data["project"]; ok {
		t.Errorf("sweep line names a project: %+v", e.Data)
	}
	NewLogShipper(logs, &fakeIssues{}, "", slog.New(slog.NewTextHandler(io.Discard, nil))).
		ShipSweep(context.Background(), ModeDryRun, 1, 1)
	if len(logs.entries) != 1 {
		t.Errorf("a shipper without a self project stored a line")
	}
}
