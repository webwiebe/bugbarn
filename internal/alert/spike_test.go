package alert

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestIsSpikeMilestone(t *testing.T) {
	t.Parallel()

	for _, n := range []int{100, 1000, 10000, 100000, 1000000} {
		if !isSpikeMilestone(n) {
			t.Errorf("%d should be a spike milestone", n)
		}
	}
	for _, n := range []int{0, 1, 10, 99, 101, 999, 1001, 5000, 20000, 100001} {
		if isSpikeMilestone(n) {
			t.Errorf("%d should not be a spike milestone", n)
		}
	}
}

func TestEvaluator_AdminNotifiedWhenEventCountCrossesMilestone(t *testing.T) {
	t.Parallel()

	notif := &fakeNotifier{configured: true}
	evaluator := NewEvaluator(newFakeRepo(nil), notif, "http://example.com", "admin@example.com", slog.Default())

	var bus domainevents.Bus
	bus.Subscribe(evaluator.HandleEvent)
	for _, count := range []int{1, 99, 100, 101, 500} {
		bus.Publish(domainevents.IssueEventRecorded{Issue: storage.Issue{ID: "issue-1", EventCount: count}, ProjectID: 7})
	}

	calls := waitForCalls(notif, 1)
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 spike notification, got %d", len(calls))
	}
	if calls[0].Condition != "event_spike" || calls[0].EmailTo != "admin@example.com" {
		t.Errorf("unexpected spike rule: %+v", calls[0])
	}
}

func TestEvaluator_NoSpikeNotificationWithoutAdminEmail(t *testing.T) {
	t.Parallel()

	notif := &fakeNotifier{configured: true}
	evaluator := NewEvaluator(newFakeRepo(nil), notif, "http://example.com", "", slog.Default())

	evaluator.HandleEvent(domainevents.IssueEventRecorded{Issue: storage.Issue{ID: "issue-1", EventCount: 100}, ProjectID: 7})

	if c := waitForCalls(notif, 1); len(c) != 0 {
		t.Errorf("expected no notification without admin email, got %d", len(c))
	}
}

func TestSpikeSubjectAndLabelCarryTheCount(t *testing.T) {
	t.Parallel()

	rule := Rule{ID: AdminRuleIDPrefix + "event_spike", Condition: "event_spike"}
	subject := alertSubject("production", rule, storage.Issue{Title: "boom", ProjectSlug: "demo", EventCount: 1000})

	if !strings.Contains(subject, "1000 events") || !strings.HasPrefix(subject, "demo: ") {
		t.Errorf("subject does not show the spike: %q", subject)
	}
	if got := conditionLabel("event_spike"); got != "Error spike" {
		t.Errorf("condition label = %q", got)
	}
	if got := alertSubject("production", Rule{ID: AdminRuleIDPrefix + "new_issue", Condition: "new_issue"}, storage.Issue{Title: "boom", ProjectSlug: "demo", EventCount: 1000}); strings.Contains(got, "events") {
		t.Errorf("plain issue subject changed: %q", got)
	}
}
