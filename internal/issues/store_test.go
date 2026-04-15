package issues

import (
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestStoreGroupsRepeatedFingerprintIntoOneIssue(t *testing.T) {
	store := NewStore()

	first := event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "request failed for user 12345",
		Exception: event.Exception{
			Type:    "panic",
			Message: "request failed for user 12345",
		},
	}

	second := event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 5, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 5, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "request failed for user 99999",
		Exception: event.Exception{
			Type:    "panic",
			Message: "request failed for user 99999",
		},
	}

	issue1 := store.AddWithFingerprint(first, "fingerprint-abc")
	issue2 := store.AddWithFingerprint(second, "fingerprint-abc")

	if issue1 != issue2 {
		t.Fatalf("expected repeated fingerprint to return the same issue")
	}
	if got, want := issue1.EventCount, 2; got != want {
		t.Fatalf("unexpected event count: got %d want %d", got, want)
	}
	if got, want := issue1.FirstSeen, first.ObservedAt; !got.Equal(want) {
		t.Fatalf("unexpected first seen: got %s want %s", got, want)
	}
	if got, want := issue1.LastSeen, second.ReceivedAt; !got.Equal(want) {
		t.Fatalf("unexpected last seen: got %s want %s", got, want)
	}
	if got, want := len(issue1.Events), 2; got != want {
		t.Fatalf("unexpected stored events: got %d want %d", got, want)
	}
	if got, want := issue1.Title, "panic: request failed for user 12345"; got != want {
		t.Fatalf("unexpected title: got %q want %q", got, want)
	}
	if got, want := issue1.NormalizedTitle, "panic: request failed for user <num>"; got != want {
		t.Fatalf("unexpected normalized title: got %q want %q", got, want)
	}
}

func TestStoreSeparatesDifferentFingerprints(t *testing.T) {
	store := NewStore()

	first := event.Event{
		ObservedAt: time.Date(2026, 4, 15, 13, 0, 0, 0, time.UTC),
		Exception: event.Exception{
			Type:    "panic",
			Message: "first failure",
		},
	}

	second := event.Event{
		ObservedAt: time.Date(2026, 4, 15, 13, 1, 0, 0, time.UTC),
		Exception: event.Exception{
			Type:    "panic",
			Message: "second failure",
		},
	}

	issue1 := store.AddWithFingerprint(first, "fingerprint-abc")
	issue2 := store.AddWithFingerprint(second, "fingerprint-def")

	if issue1 == issue2 {
		t.Fatalf("expected different fingerprints to create different issues")
	}
	if got, want := issue1.ID, "issue-000001"; got != want {
		t.Fatalf("unexpected first issue id: got %q want %q", got, want)
	}
	if got, want := issue2.ID, "issue-000002"; got != want {
		t.Fatalf("unexpected second issue id: got %q want %q", got, want)
	}
	if got, want := issue1.EventCount, 1; got != want {
		t.Fatalf("unexpected first event count: got %d want %d", got, want)
	}
	if got, want := issue2.EventCount, 1; got != want {
		t.Fatalf("unexpected second event count: got %d want %d", got, want)
	}
}
