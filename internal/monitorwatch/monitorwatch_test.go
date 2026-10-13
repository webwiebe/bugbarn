package monitorwatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/service"
	monitorsvc "github.com/wiebe-xyz/bugbarn/internal/service/monitors"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func dur(d time.Duration) *time.Duration { return &d }

type harness struct {
	t        *testing.T
	store    *storage.Store
	svc      *monitorsvc.Service
	detector *Detector
	clock    time.Time
	project  storage.Project
	failNext bool
}

// newHarness wires the real store, the real event pipeline, the reporter, the
// check-in service and the detector, with one shared fake clock.
func newHarness(t *testing.T) *harness {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	proc := ingestproc.NewProcessor(store, service.NewEventPublisher(&domainevents.Bus{}), quiet, true)
	h := &harness{t: t, store: store, clock: time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)}
	proj, err := store.CreateProject(context.Background(), "geo", "geo")
	if err != nil {
		t.Fatal(err)
	}
	h.project = proj

	persist := func(ctx context.Context, rec spool.Record) error {
		if h.failNext {
			h.failNext = false
			return errors.New("persist failed")
		}
		res := proc.PersistRecord(ctx, rec)
		if res.Outcome != ingestproc.OutcomeSuccess {
			return errors.New("not persisted: " + res.Err.Error())
		}
		return nil
	}
	rep := NewReporter(store, persist, quiet)
	rep.now = h.now
	h.svc = monitorsvc.New(store.MonitorStore, quiet)
	h.svc.SetClock(h.now)
	h.svc.SetReporter(rep)
	h.detector = NewDetector(Config{Interval: time.Second, StartupGrace: 5 * time.Minute}, store, rep, quiet)
	h.detector.SetClock(h.now)
	return h
}

func (h *harness) now() time.Time { return h.clock }

func (h *harness) advance(d time.Duration) { h.clock = h.clock.Add(d) }

func (h *harness) checkIn(status string, timings bool) {
	h.t.Helper()
	in := monitorsvc.CheckInInput{Slug: "sweep", Status: status}
	if timings {
		in.Interval, in.Grace = dur(time.Hour), dur(10*time.Minute)
	}
	if _, err := h.svc.CheckIn(context.Background(), h.project.ID, in); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) issue(kind string) domain.Issue {
	h.t.Helper()
	is, err := h.store.IssueByFingerprint(context.Background(), h.project.ID, Fingerprint("sweep", kind))
	if err != nil {
		h.t.Fatalf("issue %s: %v", kind, err)
	}
	return is
}

func (h *harness) monitor() domain.Monitor {
	h.t.Helper()
	m, err := h.store.GetMonitor(context.Background(), h.project.ID, "sweep")
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

// pastDue moves the clock beyond the startup grace and the monitor's due time.
func (h *harness) pastDue() {
	h.advance(80 * time.Minute)
	h.detector.Check(context.Background())
}

func TestMissOkMissReopensTheSameIssue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.checkIn(domain.CheckInOK, true)

	// Miss: opens an issue titled after the monitor.
	h.pastDue()
	first := h.issue(domain.MonitorStateMissed)
	if first.Status != "unresolved" || first.EventCount != 1 || first.Title != "Monitor missed: sweep" {
		t.Fatalf("after first miss: %+v", first)
	}
	if h.monitor().State != domain.MonitorStateMissed {
		t.Fatalf("monitor state %s", h.monitor().State)
	}

	// Another scan does not report again.
	h.advance(time.Minute)
	h.detector.Check(ctx)
	if got := h.issue(domain.MonitorStateMissed); got.EventCount != 1 {
		t.Fatalf("repeat scan added events: %d", got.EventCount)
	}

	// Ok: resolves the issue and records the recovery event.
	h.advance(time.Minute)
	recoveredAt := h.clock
	h.checkIn(domain.CheckInOK, false)
	resolved := h.issue(domain.MonitorStateMissed)
	if resolved.Status != "resolved" || resolved.EventCount != 2 {
		t.Fatalf("after recovery: status %s events %d", resolved.Status, resolved.EventCount)
	}
	if h.monitor().State != domain.MonitorStateOK {
		t.Fatalf("monitor state %s", h.monitor().State)
	}
	if !resolved.ResolvedAt.After(recoveredAt.Add(-time.Hour)) {
		t.Fatalf("resolved at %v", resolved.ResolvedAt)
	}

	// Miss again: the same issue regresses.
	h.pastDue()
	again := h.issue(domain.MonitorStateMissed)
	if again.ID != first.ID {
		t.Fatalf("second miss opened another issue: %s vs %s", again.ID, first.ID)
	}
	if again.Status != "unresolved" || again.RegressionCount != 1 || again.EventCount != 3 {
		t.Fatalf("after second miss: status %s regressions %d events %d", again.Status, again.RegressionCount, again.EventCount)
	}

	// Recovery resolves a regressed issue too.
	h.advance(time.Minute)
	h.checkIn(domain.CheckInOK, false)
	if got := h.issue(domain.MonitorStateMissed); got.Status != "resolved" {
		t.Fatalf("regressed issue not resolved: %s", got.Status)
	}
}

func TestErrorCheckInOpensIssueAndRecoveryResolvesIt(t *testing.T) {
	h := newHarness(t)
	h.checkIn(domain.CheckInOK, true)
	h.advance(time.Minute)
	h.checkIn(domain.CheckInError, false)
	failed := h.issue(domain.MonitorStateError)
	if failed.Status != "unresolved" || failed.Title != "Monitor failed: sweep" {
		t.Fatalf("error issue: %+v", failed)
	}

	// A second error check-in inside the repeat window adds no event.
	h.advance(time.Minute)
	h.checkIn(domain.CheckInError, false)
	if got := h.issue(domain.MonitorStateError); got.EventCount != 1 {
		t.Fatalf("repeat error added events: %d", got.EventCount)
	}

	// After a day without recovery the incident is reported again.
	h.advance(25 * time.Hour)
	h.checkIn(domain.CheckInError, false)
	if got := h.issue(domain.MonitorStateError); got.EventCount != 2 {
		t.Fatalf("daily repeat: %d events", got.EventCount)
	}

	h.advance(time.Minute)
	h.checkIn(domain.CheckInOK, false)
	if got := h.issue(domain.MonitorStateError); got.Status != "resolved" {
		t.Fatalf("status %s", got.Status)
	}
}

func TestRecoveryLeavesHandledIssuesAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.checkIn(domain.CheckInOK, true)
	h.pastDue()
	issue := h.issue(domain.MonitorStateMissed)
	if _, err := h.store.MuteIssue(domain.WithProjectID(ctx, h.project.ID), issue.ID, "forever"); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	h.checkIn(domain.CheckInOK, false)
	got := h.issue(domain.MonitorStateMissed)
	if got.Status != "muted" || got.EventCount != 1 {
		t.Fatalf("muted issue touched: status %s events %d", got.Status, got.EventCount)
	}
}

func TestNoMissInsideGraceOrStartupGrace(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.checkIn(domain.CheckInOK, true)

	// Due at +70m; at +69m nothing happens.
	h.advance(69 * time.Minute)
	h.detector.Check(ctx)
	if h.monitor().State != domain.MonitorStateOK {
		t.Fatal("marked missed inside the grace period")
	}

	// A freshly started detector waits out its startup grace.
	h.advance(10 * time.Minute)
	h.detector.SetClock(h.now)
	h.detector.Check(ctx)
	if h.monitor().State != domain.MonitorStateOK {
		t.Fatal("marked missed inside the startup grace")
	}
	h.advance(5 * time.Minute)
	h.detector.Check(ctx)
	if h.monitor().State != domain.MonitorStateMissed {
		t.Fatal("not marked missed after the startup grace")
	}
}

func TestFailedReportIsRetriedOnTheNextScan(t *testing.T) {
	h := newHarness(t)
	h.checkIn(domain.CheckInOK, true)
	h.advance(80 * time.Minute)
	h.failNext = true
	h.detector.Check(context.Background())
	if h.monitor().State != domain.MonitorStateMissed {
		t.Fatal("state should be missed even when the report failed")
	}
	if _, err := h.store.IssueByFingerprint(context.Background(), h.project.ID, Fingerprint("sweep", domain.MonitorStateMissed)); err == nil {
		t.Fatal("issue exists although the report failed")
	}
	h.advance(time.Second)
	h.detector.Check(context.Background())
	if got := h.issue(domain.MonitorStateMissed); got.EventCount != 1 {
		t.Fatalf("retry produced %d events", got.EventCount)
	}
}

func TestMissedMonitorIsReportedAgainAfterADay(t *testing.T) {
	h := newHarness(t)
	h.checkIn(domain.CheckInOK, true)
	h.pastDue()
	h.advance(23 * time.Hour)
	h.detector.Check(context.Background())
	if got := h.issue(domain.MonitorStateMissed); got.EventCount != 1 {
		t.Fatalf("reported inside the repeat window: %d", got.EventCount)
	}
	h.advance(2 * time.Hour)
	h.detector.Check(context.Background())
	if got := h.issue(domain.MonitorStateMissed); got.EventCount != 2 {
		t.Fatalf("not reported after a day: %d", got.EventCount)
	}
}
