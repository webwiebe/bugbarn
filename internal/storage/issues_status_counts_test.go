package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func persistIssue(t *testing.T, store *Store, ctx context.Context, msg string, at time.Time) Issue {
	t.Helper()
	pe := processedEventFrom(event.Event{
		ObservedAt: at,
		ReceivedAt: at.Add(time.Second),
		Severity:   "ERROR",
		Message:    msg,
		Exception:  event.Exception{Type: "CountError", Message: msg},
	})
	issue, _, _, _, err := store.PersistProcessedEvent(ctx, pe)
	if err != nil {
		t.Fatalf("persist %q: %v", msg, err)
	}
	return issue
}

func wantCounts(t *testing.T, store *Store, ctx context.Context, label string, want domain.IssueCounts) {
	t.Helper()
	got, err := store.IssueStatusCounts(ctx)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if got != want {
		t.Fatalf("%s: counts = %+v, want %+v", label, got, want)
	}
}

func TestIssueStatusCountsFollowStatusChanges(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := context.Background()
	alpha, err := store.CreateProject(base, "alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.CreateProject(base, "beta", "beta")
	if err != nil {
		t.Fatal(err)
	}
	inAlpha := domain.WithProjectID(base, alpha.ID)
	inBeta := domain.WithProjectID(base, beta.ID)
	everything := domain.WithProjectID(base, 0)
	group := domain.WithProjectIDs(base, []int64{alpha.ID, beta.ID})
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	wantCounts(t, store, inAlpha, "empty project", domain.IssueCounts{})

	a1 := persistIssue(t, store, inAlpha, "alpha one", at)
	a2 := persistIssue(t, store, inAlpha, "alpha two", at)
	a3 := persistIssue(t, store, inAlpha, "alpha three", at)
	persistIssue(t, store, inBeta, "beta one", at)
	wantCounts(t, store, inAlpha, "after create", domain.IssueCounts{Open: 3, All: 3})
	wantCounts(t, store, inBeta, "other project", domain.IssueCounts{Open: 1, All: 1})

	if _, err := store.ResolveIssue(inAlpha, a1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MuteIssue(inAlpha, a2.ID, "forever"); err != nil {
		t.Fatal(err)
	}
	wantCounts(t, store, inAlpha, "after resolve and mute", domain.IssueCounts{Open: 1, Resolved: 1, Muted: 1, All: 3})

	// A new event on a resolved issue reopens it as regressed, and still counts as open.
	persistIssue(t, store, inAlpha, "alpha one", at.Add(time.Hour))
	wantCounts(t, store, inAlpha, "after regression", domain.IssueCounts{Open: 2, Regressed: 1, Muted: 1, All: 3})

	if _, err := store.ReopenIssue(inAlpha, a1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UnmuteIssue(inAlpha, a2.ID); err != nil {
		t.Fatal(err)
	}
	wantCounts(t, store, inAlpha, "after reopen and unmute", domain.IssueCounts{Open: 3, Regressed: 1, All: 3})

	wantCounts(t, store, everything, "all projects", domain.IssueCounts{Open: 4, Regressed: 1, All: 4})
	wantCounts(t, store, group, "group scope", domain.IssueCounts{Open: 4, Regressed: 1, All: 4})
	if _, err := store.ResolveIssue(inAlpha, a3.ID); err != nil {
		t.Fatal(err)
	}
	wantCounts(t, store, everything, "all projects after resolve", domain.IssueCounts{Open: 3, Regressed: 1, Resolved: 1, All: 4})
}

func TestIssueStatusCountsMatchTheOpenFilter(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		persistIssue(t, store, ctx, fmt.Sprintf("issue %d", i), at)
	}
	first := persistIssue(t, store, ctx, "issue 0", at.Add(time.Minute))
	if _, err := store.ResolveIssue(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second := persistIssue(t, store, ctx, "issue 1", at.Add(time.Minute))
	if _, err := store.ResolveIssue(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	persistIssue(t, store, ctx, "issue 1", at.Add(2*time.Minute))

	counts, err := store.IssueStatusCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"open", "regressed", "resolved", "muted", ""} {
		listed, err := store.ListIssuesFiltered(ctx, IssueFilter{Status: status})
		if err != nil {
			t.Fatal(err)
		}
		if counts.For(status) != len(listed) {
			t.Errorf("status %q: counter says %d, list returns %d", status, counts.For(status), len(listed))
		}
	}
}

func TestProjectUsageSplitsIssuesByStatus(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := context.Background()
	proj, err := store.CreateProject(base, "split", "split")
	if err != nil {
		t.Fatal(err)
	}
	ctx := domain.WithProjectID(base, proj.ID)
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	persistIssue(t, store, ctx, "open one", at)
	done := persistIssue(t, store, ctx, "done one", at)
	back := persistIssue(t, store, ctx, "back one", at)
	if _, err := store.ResolveIssue(ctx, done.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveIssue(ctx, back.ID); err != nil {
		t.Fatal(err)
	}
	persistIssue(t, store, ctx, "back one", at.Add(time.Hour))

	usage, err := store.ProjectUsageAll(base)
	if err != nil {
		t.Fatal(err)
	}
	got := usage[proj.ID]
	want := domain.IssueCounts{Open: 2, Regressed: 1, Resolved: 1, All: 3}
	if got.IssueStatus != want || got.IssueCount != 3 {
		t.Fatalf("usage = %+v (count %d), want %+v", got.IssueStatus, got.IssueCount, want)
	}
}

// The grouped count runs on every uncached issues page, so it has to stay
// index-only. A plan assertion rather than a timing assertion: the failure is
// structural, the index stops covering the query.
func TestIssueStatusCountsUseCoveringIndex(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const query = `SELECT i.status, i.regression_count > 0, COUNT(*) FROM issues i WHERE i.project_id = 1 GROUP BY i.status, i.regression_count > 0`
	plan := queryPlan(t, store, "EXPLAIN QUERY PLAN "+query)
	if !strings.Contains(plan, "COVERING INDEX") {
		t.Fatalf("the status count reads the issues table instead of an index.\nquery: %s\nplan:  %s", query, plan)
	}
	if !strings.Contains(plan, "idx_issues_project_status_regression") {
		t.Errorf("expected idx_issues_project_status_regression.\nplan: %s", plan)
	}
}
