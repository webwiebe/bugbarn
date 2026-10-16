package storage

import (
	"context"
	"testing"
	"time"
)

func TestIssueRecordsFirstReleaseOnce(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)

	var issueID string
	for i, release := range []string{"1.4.0", "1.5.0"} {
		issue, _, _, _, err := store.PersistProcessedEvent(ctx, releaseEvent(at.Add(time.Duration(i)*time.Minute),
			map[string]any{"release": release}, nil))
		if err != nil {
			t.Fatalf("persist: %v", err)
		}
		if i == 0 && issue.FirstRelease != "1.4.0" {
			t.Errorf("new issue FirstRelease = %q, want 1.4.0", issue.FirstRelease)
		}
		issueID = issue.ID
	}

	got, err := store.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.FirstRelease != "1.4.0" {
		t.Errorf("FirstRelease = %q after a later release, want 1.4.0", got.FirstRelease)
	}

	listed, err := store.ListIssues(ctx)
	if err != nil || len(listed) != 1 || listed[0].FirstRelease != "1.4.0" {
		t.Errorf("list = %+v (err %v), want FirstRelease 1.4.0", listed, err)
	}
}

func TestIssueWithoutReleaseHasEmptyFirstRelease(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)

	issue, _, _, _, err := store.PersistProcessedEvent(ctx, releaseEvent(at, map[string]any{}, nil))
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	got, err := store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.FirstRelease != "" {
		t.Errorf("FirstRelease = %q, want empty", got.FirstRelease)
	}
}
