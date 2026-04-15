package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func TestPersistProcessedEventGroupsByFingerprintAndKeepsEventsQueryable(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	first := worker.ProcessedEvent{
		Fingerprint: "fingerprint-abc",
		Event: event.Event{
			ReceivedAt: time.Date(2026, 4, 15, 12, 0, 1, 0, time.UTC),
			ObservedAt: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
			Severity:   "ERROR",
			Message:    "request failed for user 12345",
			Exception: event.Exception{
				Type:    "panic",
				Message: "request failed for user 12345",
			},
			Attributes: map[string]any{
				"service": "api",
			},
			RawScrubbed: map[string]any{
				"attributes": map[string]any{
					"service": "api",
				},
				"resource": map[string]any{
					"host": "app-1",
				},
			},
		},
	}

	second := worker.ProcessedEvent{
		Fingerprint: "fingerprint-abc",
		Event: event.Event{
			ReceivedAt: time.Date(2026, 4, 15, 12, 5, 1, 0, time.UTC),
			ObservedAt: time.Date(2026, 4, 15, 12, 5, 0, 0, time.UTC),
			Severity:   "ERROR",
			Message:    "request failed for user 99999",
			Exception: event.Exception{
				Type:    "panic",
				Message: "request failed for user 99999",
			},
			Attributes: map[string]any{
				"service": "api",
			},
			RawScrubbed: map[string]any{
				"attributes": map[string]any{
					"service": "api",
				},
				"resource": map[string]any{
					"host": "app-1",
				},
			},
		},
	}

	issue1, event1, err := store.PersistProcessedEvent(ctx, first)
	if err != nil {
		t.Fatal(err)
	}

	issue2, event2, err := store.PersistProcessedEvent(ctx, second)
	if err != nil {
		t.Fatal(err)
	}

	if issue1.ID != issue2.ID {
		t.Fatalf("expected repeated fingerprint to reuse the same issue, got %q and %q", issue1.ID, issue2.ID)
	}
	if got, want := issue2.EventCount, 2; got != want {
		t.Fatalf("unexpected issue count: got %d want %d", got, want)
	}
	if got, want := issue2.LastSeen, second.Event.ObservedAt; !got.Equal(want) {
		t.Fatalf("unexpected last seen: got %s want %s", got, want)
	}

	issues, err := store.ListIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(issues), 1; got != want {
		t.Fatalf("unexpected issue list size: got %d want %d", got, want)
	}

	gotIssue, err := store.GetIssue(ctx, issue1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := gotIssue.EventCount, 2; got != want {
		t.Fatalf("unexpected issue event count: got %d want %d", got, want)
	}
	if got, want := gotIssue.RepresentativeEvent.Attributes["service"], "api"; got != want {
		t.Fatalf("unexpected representative event context: got %v want %q", got, want)
	}

	events, err := store.ListIssueEvents(ctx, issue1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(events), 2; got != want {
		t.Fatalf("unexpected event list size: got %d want %d", got, want)
	}
	if got, want := events[0].ID, event1.ID; got != want {
		t.Fatalf("unexpected first event id: got %q want %q", got, want)
	}
	if got, want := events[1].ID, event2.ID; got != want {
		t.Fatalf("unexpected second event id: got %q want %q", got, want)
	}

	gotEvent, err := store.GetEvent(ctx, event2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := gotEvent.IssueID, issue1.ID; got != want {
		t.Fatalf("unexpected issue link: got %q want %q", got, want)
	}
	if got, want := gotEvent.Payload.RawScrubbed["resource"].(map[string]any)["host"], "app-1"; got != want {
		t.Fatalf("unexpected stored payload context: got %v want %q", got, want)
	}
}
