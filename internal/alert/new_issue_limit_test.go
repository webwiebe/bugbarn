package alert

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestNewIssueLimiter_CapsPerProjectPerWindow(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	l := newNewIssueLimiter()
	l.now = func() time.Time { return clock }

	for i := 0; i < newIssueMailMax; i++ {
		if !l.admit("a").Send {
			t.Fatalf("mail %d of project a should go out", i+1)
		}
	}
	first := l.admit("a")
	if first.Send || !first.ScheduleSummary {
		t.Fatalf("first mail past the cap should be suppressed and schedule the summary: %+v", first)
	}
	if second := l.admit("a"); second.Send || second.ScheduleSummary {
		t.Fatalf("later suppressions schedule nothing: %+v", second)
	}
	if !l.admit("b").Send {
		t.Fatal("another project has its own budget")
	}

	if n := l.expire("a", first.WindowStart); n != 2 {
		t.Fatalf("expire reported %d suppressed, want 2", n)
	}
	if n := l.expire("a", first.WindowStart); n != 0 {
		t.Fatalf("a second expire must report 0, got %d", n)
	}
	if !l.admit("a").Send {
		t.Fatal("a fresh window sends again")
	}
}

func TestNewIssueLimiter_RolloverReportsSuppressedOnce(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	l := newNewIssueLimiter()
	l.now = func() time.Time { return clock }

	var start time.Time
	for i := 0; i < newIssueMailMax+3; i++ {
		start = l.admit("a").WindowStart
	}
	clock = clock.Add(newIssueMailWindow)

	next := l.admit("a")
	if !next.Send || next.Summarize != 3 {
		t.Fatalf("rollover should send and report 3 suppressed: %+v", next)
	}
	if n := l.expire("a", start); n != 0 {
		t.Fatalf("the old window's timer must find nothing left, got %d", n)
	}
}

func TestEvaluator_NewIssueFloodMailsFiveThenOneSummary(t *testing.T) {
	t.Parallel()

	notif := &fakeNotifier{configured: true}
	evaluator := NewEvaluator(newFakeRepo(nil), notif, "http://example.com", "admin@example.com", slog.Default())
	clock := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	evaluator.newIssues.now = func() time.Time { return clock }

	var bus domainevents.Bus
	bus.Subscribe(evaluator.HandleEvent)
	for i := 0; i < 100; i++ {
		bus.Publish(domainevents.IssueCreated{Issue: storage.Issue{ID: "i", ProjectSlug: "brandtrace"}, ProjectID: 3})
	}
	if calls := waitForCalls(notif, newIssueMailMax); len(calls) != newIssueMailMax {
		t.Fatalf("expected %d new-issue mails, got %d", newIssueMailMax, len(calls))
	}

	clock = clock.Add(newIssueMailWindow)
	bus.Publish(domainevents.IssueCreated{Issue: storage.Issue{ID: "later", ProjectSlug: "brandtrace"}, ProjectID: 3})

	calls := waitForCalls(notif, newIssueMailMax+2)
	var summary, plain int
	for _, c := range calls {
		switch c.Condition {
		case "new_issue_flood":
			summary++
		case "new_issue":
			plain++
		}
	}
	if summary != 1 || plain != newIssueMailMax+1 {
		t.Fatalf("want 1 summary and %d plain mails, got %d and %d", newIssueMailMax+1, summary, plain)
	}
	if got := conditionLabel("new_issue_flood"); !strings.Contains(got, "capped") {
		t.Errorf("label = %q", got)
	}
}
