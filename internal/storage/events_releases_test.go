package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func releaseEvent(at time.Time, attrs, resource map[string]any) worker.ProcessedEvent {
	return worker.ProcessedEvent{
		Event: event.Event{
			ObservedAt: at,
			ReceivedAt: at.Add(time.Second),
			Severity:   "ERROR",
			Message:    "lead_delivery_timeout",
			Exception:  event.Exception{Type: "lead_delivery_timeout", Message: "lead_delivery_timeout"},
			Attributes: attrs,
			Resource:   resource,
		},
		Fingerprint:         "release-breakdown-fingerprint",
		FingerprintMaterial: "lead_delivery_timeout",
	}
}

func TestExtractFacetsReleaseSpellings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		evt  event.Event
		want string
	}{
		{"release wins", event.Event{Attributes: map[string]any{"release": "1.0", "service.version": "2.0", "app.version": "3.0"}}, "1.0"},
		{"attribute service.version", event.Event{Attributes: map[string]any{"service.version": "2.0"}}, "2.0"},
		{"nested service.version", event.Event{Attributes: map[string]any{"service": map[string]any{"version": "2.1"}}}, "2.1"},
		{"resource service.version", event.Event{Resource: map[string]any{"service.version": "2.2"}}, "2.2"},
		{"app.version", event.Event{Attributes: map[string]any{"app.version": "3.0"}}, "3.0"},
		{"attribute service.version beats resource", event.Event{
			Attributes: map[string]any{"service.version": "2.0"},
			Resource:   map[string]any{"service.version": "9.9"},
		}, "2.0"},
		{"none", event.Event{Attributes: map[string]any{"plugin_version": "0.2.0"}}, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractFacets(tc.evt)["release"]; got != tc.want {
				t.Fatalf("release facet = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIssueReleases(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	var issueID string
	persist := func(minutes int, attrs, resource map[string]any) {
		t.Helper()
		issue, _, _, _, err := store.PersistProcessedEvent(ctx, releaseEvent(base.Add(time.Duration(minutes)*time.Minute), attrs, resource))
		if err != nil {
			t.Fatal(err)
		}
		issueID = issue.ID
	}
	persist(0, map[string]any{"release": "0.2.0"}, nil)
	persist(1, map[string]any{"service.version": "0.2.0"}, nil)
	persist(2, map[string]any{"release": "0.2.0"}, nil)
	persist(3, nil, map[string]any{"service.version": "0.3.0"})
	persist(4, map[string]any{"plugin_version": "0.9.9"}, nil) // no release

	releases, examined, err := store.IssueReleases(ctx, issueID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if examined != 5 {
		t.Fatalf("examined = %d, want 5", examined)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %+v, want 2", releases)
	}
	if r := releases[0]; r.Release != "0.3.0" || r.Events != 1 || !r.LastSeen.Equal(base.Add(3*time.Minute)) {
		t.Errorf("first = %+v, want 0.3.0 with 1 event last seen at +3m", r)
	}
	if r := releases[1]; r.Release != "0.2.0" || r.Events != 3 ||
		!r.FirstSeen.Equal(base) || !r.LastSeen.Equal(base.Add(2*time.Minute)) {
		t.Errorf("second = %+v, want 0.2.0 with 3 events between +0m and +2m", r)
	}

	// Only the latest events are looked at.
	releases, examined, err = store.IssueReleases(ctx, issueID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if examined != 2 || len(releases) != 1 || releases[0].Release != "0.3.0" {
		t.Errorf("sample of 2: examined=%d releases=%+v, want the 0.3.0 event only", examined, releases)
	}
}

func TestIssueReleasesNoneAndMissingIssue(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	issue, _, _, _, err := store.PersistProcessedEvent(ctx, releaseEvent(time.Now().UTC(), nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	releases, examined, err := store.IssueReleases(ctx, issue.ID, 500)
	if err != nil || examined != 1 || len(releases) != 0 {
		t.Fatalf("releases=%+v examined=%d err=%v, want none from 1 event", releases, examined, err)
	}
	if _, _, err := store.IssueReleases(ctx, "NOPE-999", 500); err == nil {
		t.Fatal("expected an error for an unknown issue")
	}
}

func TestIssueReleasesUsesIssueIndex(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rows, err := store.readDB().Query(`EXPLAIN QUERY PLAN
SELECT event_json FROM events WHERE project_id = ? AND issue_id = ? ORDER BY id DESC LIMIT ?`, 1, 1, 500)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "idx_events_issue_id") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("plan = %q, want idx_events_issue_id without a sort", plan)
	}
}
