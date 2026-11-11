package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// ListIssueEvents pages an issue's events newest first by event id, answers
// each page in ascending order, and reports whether older events remain.
func TestListIssueEventsPagesNewestFirstAnswersAscending(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()

	var issueID string
	var ids []string
	for i := 0; i < 5; i++ {
		at := time.Date(2026, 4, 15, 12, i, 0, 0, time.UTC)
		msg := fmt.Sprintf("request %d failed", i)
		issue, ev, _, _, err := store.PersistProcessedEvent(ctx, processedEventFrom(event.Event{
			ReceivedAt: at, ObservedAt: at, Severity: "ERROR", Message: msg,
			Exception: event.Exception{Type: "panic", Message: msg},
		}))
		if err != nil {
			t.Fatal(err)
		}
		issueID = issue.ID
		ids = append(ids, ev.ID)
	}

	page, more, err := store.ListIssueEvents(ctx, issueID, 2, 0)
	if err != nil || !more || len(page) != 2 {
		t.Fatalf("first page = %d events, more=%v, err=%v; want 2, true", len(page), more, err)
	}
	if page[0].ID != ids[3] || page[1].ID != ids[4] {
		t.Fatalf("first page ids = %s,%s; want the newest two ascending (%s,%s)", page[0].ID, page[1].ID, ids[3], ids[4])
	}

	var before int64
	if _, err := fmt.Sscanf(page[0].ID, "event-%d", &before); err != nil {
		t.Fatalf("event id %q is not numeric: %v", page[0].ID, err)
	}
	older, more, err := store.ListIssueEvents(ctx, issueID, 2, before)
	if err != nil || !more || len(older) != 2 || older[0].ID != ids[1] || older[1].ID != ids[2] {
		t.Fatalf("second page = %+v, more=%v, err=%v; want %s,%s with more", older, more, err, ids[1], ids[2])
	}

	last, more, err := store.ListIssueEvents(ctx, issueID, 5, before-2)
	if err != nil || more || len(last) != 1 || last[0].ID != ids[0] {
		t.Fatalf("last page = %+v, more=%v, err=%v; want only %s without more", last, more, err, ids[0])
	}

	all, more, err := store.ListIssueEvents(ctx, issueID, 0, 0)
	if err != nil || more || len(all) != 5 {
		t.Fatalf("default limit = %d events, more=%v, err=%v; want all 5 without more", len(all), more, err)
	}
}
