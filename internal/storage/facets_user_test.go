package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestUserIDIsAFacetAndNotInTheFingerprint(t *testing.T) {
	t.Parallel()

	assertFacet(t, extractFacets(event.Event{User: event.UserContext{ID: "u-123"}}), "user.id", "u-123")
	assertFacet(t, extractFacets(event.Event{Attributes: map[string]any{"user.id": "u-9"}}), "user.id", "u-9")
	if _, ok := extractFacets(event.Event{User: event.UserContext{Email: "a@b.c"}})["user.id"]; ok {
		t.Fatal("an event without a user id must not carry a user.id facet")
	}

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	mk := func(uid string, at time.Time) event.Event {
		return event.Event{
			ObservedAt: at,
			ReceivedAt: at.Add(time.Second),
			Severity:   "ERROR",
			Message:    "boom",
			Exception:  event.Exception{Type: "Error", Message: "boom"},
			User:       event.UserContext{ID: uid},
		}
	}
	base := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	issueA, _, _, _, err := store.PersistProcessedEvent(ctx, processedEventFrom(mk("u-123", base)))
	if err != nil {
		t.Fatal(err)
	}
	issueB, _, _, _, err := store.PersistProcessedEvent(ctx, processedEventFrom(mk("u-456", base.Add(time.Minute))))
	if err != nil {
		t.Fatal(err)
	}
	if issueA.ID != issueB.ID {
		t.Fatalf("different users split one error into issues %s and %s", issueA.ID, issueB.ID)
	}

	for uid, want := range map[string]int{"u-123": 1, "u-456": 1, "u-nobody": 0} {
		got, err := store.ListIssuesFiltered(ctx, IssueFilter{Facets: map[string]string{"user.id": uid}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Fatalf("user.id=%s: want %d issues, got %d", uid, want, len(got))
		}
		if want == 1 && got[0].ID != issueA.ID {
			t.Fatalf("user.id=%s: got issue %s, want %s", uid, got[0].ID, issueA.ID)
		}
	}
}
