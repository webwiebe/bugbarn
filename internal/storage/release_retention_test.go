package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// addReleases inserts n markers for the project, each observed earlier than the
// last, so insertion order and observed order disagree.
func addReleases(t *testing.T, store *Store, projectID int64, prefix string, n int) {
	t.Helper()
	ctx := domain.WithProjectID(context.Background(), projectID)
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		if _, err := store.CreateRelease(ctx, Release{
			Name: fmt.Sprintf("%s-%d", prefix, i), ObservedAt: at.Add(-time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func releaseNames(t *testing.T, store *Store, projectID int64) map[string]bool {
	t.Helper()
	list, err := store.ListReleases(domain.WithProjectID(context.Background(), projectID))
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(list))
	for _, r := range list {
		names[r.Name] = true
	}
	return names
}

// Each project keeps its newest rows by insertion; observed_at does not decide.
// A project at or under the cap is left alone, and limit bounds one call.
func TestPruneReleasesKeepsTheNewestRowsOfEachProject(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	alpha, err := store.CreateProject(ctx, "Alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.CreateProject(ctx, "Beta", "beta")
	if err != nil {
		t.Fatal(err)
	}
	addReleases(t, store, alpha.ID, "a", 6)
	addReleases(t, store, beta.ID, "b", 3)

	n, err := store.PruneReleases(ctx, 3, 2)
	if err != nil || n != 2 {
		t.Fatalf("first call deleted %d (err %v), want 2", n, err)
	}
	if n, err = store.PruneReleases(ctx, 3, 100); err != nil || n != 1 {
		t.Fatalf("second call deleted %d (err %v), want 1", n, err)
	}
	if n, err = store.PruneReleases(ctx, 3, 100); err != nil || n != 0 {
		t.Fatalf("third call deleted %d (err %v), want 0", n, err)
	}

	got := releaseNames(t, store, alpha.ID)
	for _, want := range []string{"a-3", "a-4", "a-5"} {
		if !got[want] {
			t.Errorf("alpha lost %s, one of its last three inserted; kept %v", want, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("alpha kept %d rows, want 3", len(got))
	}
	if got := releaseNames(t, store, beta.ID); len(got) != 3 {
		t.Errorf("beta, at the cap, kept %d rows, want 3", len(got))
	}
}

// The hourly check for projects over the cap is answered from the index alone.
func TestProjectsOverReleaseCapUsesACoveringIndex(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	rows, err := store.db.Query("EXPLAIN QUERY PLAN "+projectsOverReleaseCapQuery, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if joined := strings.Join(plan, "; "); !strings.Contains(joined, "COVERING INDEX") {
		t.Fatalf("plan %q does not use a covering index", joined)
	}
}

// ListRecentReleases returns the newest rows first, at most limit of them.
func TestListRecentReleasesIsBounded(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	proj, err := store.CreateProject(context.Background(), "Alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	addReleases(t, store, proj.ID, "a", 5)
	list, err := store.ListRecentReleases(domain.WithProjectID(context.Background(), proj.ID), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "a-0" || list[1].Name != "a-1" {
		t.Fatalf("got %d rows %v, want a-0 and a-1, newest observed first", len(list), list)
	}
	if _, err := store.ListRecentReleases(context.Background(), 0); err == nil {
		t.Fatal("a zero limit was accepted")
	}
}
