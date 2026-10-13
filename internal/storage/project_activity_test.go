package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func insertIssueSeen(t *testing.T, store *Store, projectID int64, n int, lastSeen time.Time) {
	t.Helper()
	ts := lastSeen.UTC().Format(time.RFC3339)
	_, err := store.db.ExecContext(context.Background(), `
		INSERT INTO issues (project_id, fingerprint, fingerprint_material, title, normalized_title,
			exception_type, first_seen, last_seen, event_count, representative_event_json, issue_number)
		VALUES (?, ?, '', 'issue', 'issue', 'Error', ?, ?, 1, '{}', ?)`,
		projectID, fmt.Sprintf("fp-%d-%d", projectID, n), ts, ts, n)
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectActivitySplitsRecentFromEarlier(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	healthy, _ := store.CreateProject(ctx, "Healthy", "healthy")
	silent, _ := store.CreateProject(ctx, "Silent", "silent")
	dormant, _ := store.CreateProject(ctx, "Dormant", "dormant")

	// healthy: still reporting yesterday
	insertIssueSeen(t, store, healthy.ID, 1, now.Add(-24*time.Hour))
	insertIssueSeen(t, store, healthy.ID, 2, now.Add(-10*24*time.Hour))
	// silent: three issues active 8 to 20 days ago, nothing since
	for i := 1; i <= 3; i++ {
		insertIssueSeen(t, store, silent.ID, i, now.Add(-time.Duration(7+i*3)*24*time.Hour))
	}
	// dormant: last activity 90 days ago, outside the lookback
	insertIssueSeen(t, store, dormant.ID, 1, now.Add(-90*24*time.Hour))

	got, err := store.ProjectActivity(ctx, now, 7*24*time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bySlug := map[string]ProjectActivity{}
	for _, a := range got {
		bySlug[a.Slug] = a
	}

	if a := bySlug["healthy"]; a.Recent != 1 || a.Earlier != 1 {
		t.Errorf("healthy = %+v, want recent 1 earlier 1", a)
	}
	if a := bySlug["silent"]; a.Recent != 0 || a.Earlier != 3 {
		t.Errorf("silent = %+v, want recent 0 earlier 3", a)
	}
	if _, ok := bySlug["dormant"]; ok {
		t.Errorf("a project with no activity inside the lookback must not be listed: %+v", bySlug["dormant"])
	}
}
