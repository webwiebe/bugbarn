package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
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
	first := processedEventFrom(event.Event{
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
	})

	second := processedEventFrom(event.Event{
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
	})

	issue1, event1, _, _, err := store.PersistProcessedEvent(ctx, first)
	if err != nil {
		t.Fatal(err)
	}

	issue2, event2, _, _, err := store.PersistProcessedEvent(ctx, second)
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

	events, _, err := store.ListIssueEvents(ctx, issue1.ID, 50, 0)
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
	if got := gotIssue.FingerprintMaterial; got == "" {
		t.Fatal("expected issue fingerprint material")
	}
	if got := gotIssue.FingerprintExplanation; len(got) == 0 {
		t.Fatal("expected issue fingerprint explanation")
	}
	if got := event1.FingerprintMaterial; got == "" {
		t.Fatal("expected event fingerprint material")
	}
	if got := event1.FingerprintExplanation; len(got) == 0 {
		t.Fatal("expected event fingerprint explanation")
	}
}

func TestResolveIssueReopensOnRegressionAndLiveEventsAreWindowed(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	first := processedEventForIssue(time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC), "request failed for user 12345")
	second := processedEventForIssue(base.Add(-5*time.Minute), "request failed for user 67890")

	issue, _, _, _, err := store.PersistProcessedEvent(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, second); err != nil {
		t.Fatal(err)
	}

	resolved, err := store.ResolveIssue(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "resolved" {
		t.Fatalf("unexpected resolved status: %q", resolved.Status)
	}
	if resolved.ResolvedAt.IsZero() {
		t.Fatal("expected resolved timestamp")
	}

	regression := processedEventForIssue(base, "request failed for user 99999")
	regressionIssue, regressionEvent, _, _, err := store.PersistProcessedEvent(ctx, regression)
	if err != nil {
		t.Fatal(err)
	}
	if regressionIssue.Status != "unresolved" {
		t.Fatalf("unexpected regression status: %q", regressionIssue.Status)
	}
	if regressionIssue.RegressionCount != 1 {
		t.Fatalf("unexpected regression count: %d", regressionIssue.RegressionCount)
	}
	if !regressionEvent.Regressed {
		t.Fatal("expected event to be marked as regression")
	}
	if regressionIssue.LastRegressedAt.IsZero() {
		t.Fatal("expected regression timestamp")
	}

	oldEvent := processedEventForIssue(time.Date(2026, 4, 15, 8, 0, 0, 0, time.UTC), "stale error")
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, oldEvent); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListRecentEvents(ctx, 50, time.Date(2026, 4, 15, 11, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range events {
		if item.ObservedAt.Before(time.Date(2026, 4, 15, 11, 30, 0, 0, time.UTC)) && item.ReceivedAt.Before(time.Date(2026, 4, 15, 11, 30, 0, 0, time.UTC)) {
			t.Fatalf("unexpected stale live event: %#v", item)
		}
	}
}

func processedEventFrom(evt event.Event) worker.ProcessedEvent {
	snapshot := fingerprint.SnapshotFor(evt)
	fp := fingerprint.Fingerprint(evt)
	evt.Fingerprint = fp
	evt.FingerprintMaterial = snapshot.Material
	evt.FingerprintExplanation = snapshot.Explanation
	return worker.ProcessedEvent{
		Event:                  evt,
		Fingerprint:            fp,
		FingerprintMaterial:    snapshot.Material,
		FingerprintExplanation: snapshot.Explanation,
	}
}

func processedEventForIssue(observed time.Time, message string) worker.ProcessedEvent {
	evt := event.Event{
		ObservedAt: observed,
		ReceivedAt: observed.Add(1 * time.Second),
		Severity:   "ERROR",
		Message:    message,
		Exception: event.Exception{
			Type:    "panic",
			Message: message,
		},
		Attributes: map[string]any{
			"service.name": "api",
			"release":      "v1.2.3",
		},
	}
	return processedEventFrom(evt)
}

func TestConcurrentReads(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		pe := processedEventForIssue(
			time.Now().UTC().Add(time.Duration(-i)*time.Minute),
			fmt.Sprintf("concurrent test error %d", i),
		)
		pe.Fingerprint = fmt.Sprintf("concurrent-fp-%d", i)
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, pe); err != nil {
			t.Fatal(err)
		}
	}

	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := store.ListIssuesFiltered(ctx, IssueFilter{Status: "open", Limit: 10})
			errs <- err
		}()
	}
	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent read failed: %v", err)
		}
	}
}

func TestRegressedFirstOrdering(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create two issues.
	peA := processedEventForIssue(time.Now().UTC(), "normal issue")
	peA.Fingerprint = "regressed-order-a"
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, peA); err != nil {
		t.Fatal(err)
	}

	peB := processedEventForIssue(time.Now().UTC().Add(-time.Minute), "will regress")
	peB.Fingerprint = "regressed-order-b"
	issueB, _, _, _, err := store.PersistProcessedEvent(ctx, peB)
	if err != nil {
		t.Fatal(err)
	}

	// Mute B with until_regression, then trigger regression.
	if _, err := store.MuteIssue(ctx, issueB.ID, "until_regression"); err != nil {
		t.Fatal(err)
	}
	peB2 := processedEventForIssue(time.Now().UTC(), "will regress again")
	peB2.Fingerprint = "regressed-order-b"
	_, _, _, regressed, err := store.PersistProcessedEvent(ctx, peB2)
	if err != nil {
		t.Fatal(err)
	}
	if !regressed {
		t.Fatal("expected regression")
	}

	issues, err := store.ListIssuesFiltered(ctx, IssueFilter{Status: "open", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) < 2 {
		t.Fatalf("expected at least 2 issues, got %d", len(issues))
	}
	if issues[0].Status != "regressed" {
		t.Errorf("expected first issue to be regressed, got %q", issues[0].Status)
	}
}
