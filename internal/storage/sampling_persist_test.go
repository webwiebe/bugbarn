package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// persistBurst drives n identical events through the real write path, the way
// a client stuck in a loop would, and returns the issue's row id.
func persistBurst(t *testing.T, store *Store, n int) int64 {
	t.Helper()
	ctx := context.Background()
	var issueID int64
	for i := 0; i < n; i++ {
		issue, _, _, _, err := store.PersistProcessedEvent(ctx, processedEventFrom(event.Event{
			ObservedAt: time.Date(2026, 9, 9, 12, 0, 0, i, time.UTC),
			ReceivedAt: time.Date(2026, 9, 9, 12, 0, 0, i, time.UTC),
			Severity:   "ERROR",
			Message:    "sampling burst",
			Exception:  event.Exception{Type: "BurstError", Message: "sampling burst"},
			Resource:   map[string]any{"host.name": "web-01"},
			Attributes: map[string]any{"environment": "production"},
		}))
		if err != nil {
			t.Fatalf("persist event %d: %v", i, err)
		}
		if issueID == 0 {
			var err error
			issueID, err = store.IssueRowIDByDisplayID(ctx, issue.ID)
			if err != nil {
				t.Fatalf("resolve issue row id: %v", err)
			}
		}
	}
	return issueID
}

func scalar(t *testing.T, store *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := store.DB().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

// The whole point: past the threshold, stored rows stop tracking events while
// the issue's own count keeps counting every one of them.
func TestPersistSamplesPastTheThreshold(t *testing.T) {
	t.Parallel()

	store, err := open(filepath.Join(t.TempDir(), "bugbarn.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.SetSampleAfter(100)

	const burst = 3000
	issueID := persistBurst(t, store, burst)

	stored := scalar(t, store, `SELECT COUNT(*) FROM events WHERE issue_id = ?`, issueID)
	if stored >= burst {
		t.Fatalf("stored %d rows for %d events; nothing was sampled", stored, burst)
	}
	// 100 below the threshold, then 1-in-10 for the remaining 2,900.
	if stored < 100 || stored > 500 {
		t.Errorf("stored %d rows, want roughly 100 + 2900/10", stored)
	}

	// The issue's lifetime count is exact. This is what alerting reads, and it is
	// the number a human is told the error happened.
	var eventCount int64
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT event_count FROM issues WHERE id = ?`, issueID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != burst {
		t.Errorf("issues.event_count = %d, want exactly %d", eventCount, burst)
	}

	// And the weights reconstruct the volume, to within one rung.
	summed := scalar(t, store, `SELECT COALESCE(SUM(sample_weight), 0) FROM events WHERE issue_id = ?`, issueID)
	if summed > burst || summed <= burst-10 {
		t.Errorf("SUM(sample_weight) = %d, want within (%d, %d]", summed, burst-10, burst)
	}
}

// Sampling must never cost us the ability to see what the error was.
func TestSampledIssueKeepsItsEvidence(t *testing.T) {
	t.Parallel()

	store, err := open(filepath.Join(t.TempDir(), "bugbarn.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.SetSampleAfter(10)

	issueID := persistBurst(t, store, 500)

	var representative string
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT representative_event_json FROM issues WHERE id = ?`, issueID).Scan(&representative); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(representative, "BurstError") {
		t.Errorf("issue lost its representative event: %q", representative)
	}
	if stored := scalar(t, store, `SELECT COUNT(*) FROM events WHERE issue_id = ?`, issueID); stored == 0 {
		t.Error("issue has no stored events at all")
	}

	// Facets are written for sampled-out events too: an issue seen on a new host
	// or environment must become findable by it even if that event was not stored.
	ctx := context.Background()
	values, err := store.ListFacetValues(ctx, store.DefaultProjectID(), "attributes.environment")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "production" {
		t.Errorf("attributes.environment = %v, want [production]", values)
	}
}

// A project that opts out keeps everything, whatever the deployment default is.
func TestProjectCanOptOutOfSampling(t *testing.T) {
	t.Parallel()

	store, err := open(filepath.Join(t.TempDir(), "bugbarn.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.SetSampleAfter(10)
	ctx := context.Background()

	slug := "default"
	if err := store.UpdateProjectLimits(ctx, slug, nil, SamplingOff); err != nil {
		t.Fatalf("opt out: %v", err)
	}

	const burst = 200
	issueID := persistBurst(t, store, burst)

	if stored := scalar(t, store, `SELECT COUNT(*) FROM events WHERE issue_id = ?`, issueID); stored != burst {
		t.Errorf("stored %d rows for %d events; an opted-out project must keep everything", stored, burst)
	}
}

// Derived counts read weights, so they report real volume rather than how many
// rows survived sampling.
func TestDerivedCountsReportTrueVolumeUnderSampling(t *testing.T) {
	t.Parallel()

	store, err := open(filepath.Join(t.TempDir(), "bugbarn.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.SetSampleAfter(50)
	ctx := context.Background()

	const burst = 1000
	persistBurst(t, store, burst)

	projectID := store.DefaultProjectID()
	stored := scalar(t, store, `SELECT COUNT(*) FROM events WHERE project_id = ?`, projectID)
	if stored >= burst {
		t.Fatalf("stored %d rows for %d events; nothing was sampled, so this proves nothing", stored, burst)
	}

	usage, err := store.ProjectUsageAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := int64(usage[projectID].EventCount); got > burst || got <= burst-10 {
		t.Errorf("ProjectUsageAll event count = %d, want close to %d (not the %d stored rows)", got, burst, stored)
	}

	digest, err := store.WeeklyDigest(ctx, projectID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got := int64(digest.TotalEvents); got > burst || got <= burst-10 {
		t.Errorf("digest TotalEvents = %d, want close to %d (not the %d stored rows)", got, burst, stored)
	}
	if len(digest.TopIssues) == 0 {
		t.Fatal("digest listed no top issues")
	}
	if got := int64(digest.TopIssues[0].EventCount); got > burst || got <= burst-10 {
		t.Errorf("digest top issue count = %d, want close to %d", got, burst)
	}
}
