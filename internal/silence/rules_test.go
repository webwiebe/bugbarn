package silence

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

const sortableLayout = "2006-01-02T15:04:05.000000000Z07:00"

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t     *testing.T
	store *storage.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &fixture{t: t, store: store}
}

func (f *fixture) project(slug string) storage.Project {
	f.t.Helper()
	p, err := f.store.Domains().Projects.CreateProject(context.Background(), slug, slug)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

// logs writes one log row per time.
func (f *fixture) logs(projectID int64, times []time.Time) {
	f.t.Helper()
	for _, at := range times {
		if _, err := f.store.DB().Exec(
			`INSERT INTO log_entries (project_id, received_at, message) VALUES (?, ?, 'line')`,
			projectID, at.UTC().Format(sortableLayout)); err != nil {
			f.t.Fatal(err)
		}
	}
}

// events stores one event per time through the real persist path.
func (f *fixture) events(projectID int64, times []time.Time) {
	f.t.Helper()
	ctx := domain.WithProjectID(context.Background(), projectID)
	for i, at := range times {
		msg := fmt.Sprintf("failure %d", i%3)
		evt := event.Event{
			ReceivedAt: at, ObservedAt: at, Severity: "ERROR", Message: msg,
			Exception: event.Exception{Type: "panic", Message: msg},
		}
		snap := fingerprint.SnapshotFor(evt)
		fp := fingerprint.Fingerprint(evt)
		evt.Fingerprint, evt.FingerprintMaterial, evt.FingerprintExplanation = fp, snap.Material, snap.Explanation
		if _, _, _, _, err := f.store.PersistProcessedEvent(ctx, worker.ProcessedEvent{
			Event: evt, Fingerprint: fp, FingerprintMaterial: snap.Material, FingerprintExplanation: snap.Explanation,
		}); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) keyUsedAt(projectID int64, scope string, at time.Time) {
	f.t.Helper()
	key, err := f.store.Domains().APIKeys.CreateAPIKey(context.Background(), "k", projectID, fmt.Sprintf("sha-%d-%s", projectID, scope), scope)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, at.UTC().Format(sortableLayout), key.ID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) check() []Finding {
	rules := NewRules(DefaultRulesConfig(), StoreSource{ProjectStore: f.store.Domains().Projects},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return rules.Check(context.Background(), now)
}

// every returns times at a fixed step from start (inclusive) up to end (exclusive), oldest first.
func every(start, end time.Time, step time.Duration) []time.Time {
	var out []time.Time
	for at := start; at.Before(end); at = at.Add(step) {
		out = append(out, at)
	}
	return out
}

func findingFor(found []Finding, slug string) []Finding {
	var out []Finding
	for _, f := range found {
		if f.Slug == slug {
			out = append(out, f)
		}
	}
	return out
}

// github-tamagotchi, 2026-11-01: about 70 log rows an hour for days, then
// nothing for 55.9 hours. The log rule fires and names that gap.
func TestLogStopFiresTheLogRule(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("github-tamagotchi")
	stop := now.Add(-(55*time.Hour + 54*time.Minute))
	f.logs(p.ID, every(stop.Add(-5*24*time.Hour), stop, time.Hour/70))

	got := findingFor(f.check(), "github-tamagotchi")
	if len(got) != 1 || got[0].Rule != RuleLogs {
		t.Fatalf("findings = %+v, want exactly the log rule", got)
	}
	if got[0].Gap < 55*time.Hour || got[0].Gap > 56*time.Hour {
		t.Fatalf("gap = %s, want about 55.9h", got[0].Gap)
	}
	if got[0].Volume < 150 {
		t.Fatalf("volume = %d, want the 72h window's rows", got[0].Volume)
	}
}

// A project with busy logs and a falling error rate stays quiet until the
// newest event is 7 days old.
func TestBusyLogsFewEventsStayQuietBeforeSevenDays(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("busy-logs")
	f.logs(p.ID, every(now.Add(-4*24*time.Hour), now, time.Minute))
	// Events on 8 distinct days, the newest 6 days ago.
	var ev []time.Time
	for d := 6; d <= 13; d++ {
		ev = append(ev, now.Add(-time.Duration(d)*24*time.Hour))
	}
	f.events(p.ID, ev)

	if got := findingFor(f.check(), "busy-logs"); len(got) != 0 {
		t.Fatalf("findings before 7 days = %+v, want none", got)
	}

	// Same project 8 days after its last event: the event rule fires.
	f2 := newFixture(t)
	p2 := f2.project("busy-logs")
	f2.logs(p2.ID, every(now.Add(-4*24*time.Hour), now, time.Minute))
	var ev2 []time.Time
	for d := 8; d <= 15; d++ {
		ev2 = append(ev2, now.Add(-time.Duration(d)*24*time.Hour))
	}
	f2.events(p2.ID, ev2)
	got := findingFor(f2.check(), "busy-logs")
	if len(got) != 1 || got[0].Rule != RuleEvents {
		t.Fatalf("findings at 8 days = %+v, want the event rule", got)
	}
}

// A project with one event a week reaches about 4 distinct days in 30 and is
// not reported, even when its newest event is old.
func TestOneEventAWeekIsNotReported(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("weekly")
	var ev []time.Time
	for d := 9; d <= 30; d += 7 {
		ev = append(ev, now.Add(-time.Duration(d)*24*time.Hour))
	}
	f.events(p.ID, ev)

	if got := findingFor(f.check(), "weekly"); len(got) != 0 {
		t.Fatalf("findings = %+v, want none", got)
	}
}

// The legacy professionals project: its only key row was unused for 350.9
// hours and its newest events came from BugBarn's own monitors. The key signal
// is evidence only, so with recent reporting nothing fires, and with a key
// alone nothing fires either.
func TestUnusedLegacyKeyRowNeverFires(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("professionals")
	f.keyUsedAt(p.ID, "ingest", now.Add(-351*time.Hour))
	f.logs(p.ID, every(now.Add(-2*24*time.Hour), now, time.Minute))
	f.events(p.ID, []time.Time{now.Add(-2 * time.Hour)})

	if got := findingFor(f.check(), "professionals"); len(got) != 0 {
		t.Fatalf("findings = %+v, want none", got)
	}

	f2 := newFixture(t)
	q := f2.project("key-only")
	f2.keyUsedAt(q.ID, "ingest", now.Add(-351*time.Hour))
	if got := findingFor(f2.check(), "key-only"); len(got) != 0 {
		t.Fatalf("a key alone fired: %+v", got)
	}
}

// The newest ingest key use rides along as evidence; a read-scope key does not count.
func TestFindingCarriesTheNewestIngestKeyUse(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("evidence")
	stop := now.Add(-10 * time.Hour)
	f.logs(p.ID, every(stop.Add(-4*24*time.Hour), stop, 5*time.Minute))
	used := now.Add(-30 * time.Hour)
	f.keyUsedAt(p.ID, "ingest", used)
	f.keyUsedAt(p.ID, "read", now.Add(-time.Hour))

	got := findingFor(f.check(), "evidence")
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	if !got[0].KeyLastUsed.Equal(used) {
		t.Fatalf("key last used = %s, want %s", got[0].KeyLastUsed, used)
	}
}

// Dead projects (past the 30-day ceiling) and an office-hours app overnight stay quiet.
func TestCeilingAndDailyRhythmStayQuiet(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dead := f.project("backend")
	f.logs(dead.ID, every(now.Add(-200*24*time.Hour), now.Add(-176*24*time.Hour), time.Minute*10))

	office := f.project("office-hours")
	var rows []time.Time
	// Five working days, 08:00 to 17:00 every 5 minutes, the last one ending
	// 19 hours before now: the gap clears 6 hours and the row floor, and only
	// the 3x-longest-gap test (the usual overnight gap is 15 hours) keeps it quiet.
	for d := 5; d >= 1; d-- {
		day := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC).Add(-time.Duration(d) * 24 * time.Hour)
		rows = append(rows, every(day, day.Add(9*time.Hour), 5*time.Minute)...)
	}
	f.logs(office.ID, rows)

	got := f.check()
	if d := findingFor(got, "backend"); len(d) != 0 {
		t.Fatalf("dead project fired: %+v", d)
	}
	if o := findingFor(got, "office-hours"); len(o) != 0 {
		t.Fatalf("office-hours app fired overnight: %+v", o)
	}
}

func TestModeOffEvaluatesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := f.project("silent")
	stop := now.Add(-48 * time.Hour)
	f.logs(p.ID, every(stop.Add(-4*24*time.Hour), stop, 5*time.Minute))

	cfg := DefaultRulesConfig()
	cfg.Mode = ParseMode("off")
	rules := NewRules(cfg, StoreSource{ProjectStore: f.store.Domains().Projects}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := rules.Check(context.Background(), now); len(got) != 0 {
		t.Fatalf("off mode returned %+v", got)
	}
	if ParseMode("") != ModeDryRun || ParseMode("dry-run") != ModeDryRun || ParseMode("file") != ModeFile || ParseMode("bogus") != ModeOff {
		t.Fatal("ParseMode: empty and dry-run are dry run, file is file, anything else is off")
	}
}
