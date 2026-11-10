package storage

import (
	"testing"
	"time"
)

func TestParseIssueTimesRequiresSeenTimesAndToleratesEmpty(t *testing.T) {
	t.Parallel()

	first := formatTime(time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC))
	last := formatTime(time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC))

	var issue Issue
	if err := parseIssueTimes(&issue, first, last, "", "", ""); err != nil {
		t.Fatalf("parseIssueTimes: %v", err)
	}
	if !issue.FirstSeen.Equal(time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)) ||
		!issue.LastSeen.Equal(time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("seen times = %v, %v", issue.FirstSeen, issue.LastSeen)
	}
	if !issue.ResolvedAt.IsZero() || !issue.ReopenedAt.IsZero() || !issue.LastRegressedAt.IsZero() {
		t.Fatalf("empty optional times must stay zero: %+v", issue)
	}

	if err := parseIssueTimes(&Issue{}, "not a time", last, "", "", ""); err == nil {
		t.Fatal("bad first_seen must fail")
	}
	if err := parseIssueTimes(&Issue{}, first, "not a time", "", "", ""); err == nil {
		t.Fatal("bad last_seen must fail")
	}
	if err := parseIssueTimes(&Issue{}, first, last, "junk", "junk", "junk"); err != nil {
		t.Fatalf("bad optional times must be ignored: %v", err)
	}
}
