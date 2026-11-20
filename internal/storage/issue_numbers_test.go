package storage

import (
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func persistFingerprints(t *testing.T, store *Store, projectID int64, env string, fingerprints ...string) {
	t.Helper()
	ctx := domain.WithProjectID(t.Context(), projectID)
	for _, f := range fingerprints {
		p := releaseEvent(time.Now().UTC(), map[string]any{"environment": env}, nil)
		p.Fingerprint, p.FingerprintMaterial = f, f
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, p); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
}

func displayDuplicates(t *testing.T, store *Store, projectID int64) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM (SELECT issue_number FROM issues WHERE project_id = ? GROUP BY issue_number HAVING COUNT(*) > 1)`,
		projectID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Issues moved in by a merge take numbers after the target's counter, so each
// display id names one issue.
func TestMergeProjectsKeepsIssueNumbersUnique(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	source, _ := store.CreateProject(ctx, "Source", "source-proj")
	target, _ := store.CreateProject(ctx, "Target", "target-proj")
	persistFingerprints(t, store, target.ID, "production", "t1", "t2", "t3")
	persistFingerprints(t, store, source.ID, "e2e", "s1", "s2")

	if err := store.MergeProjects(ctx, "source-proj", "target-proj"); err != nil {
		t.Fatal(err)
	}
	if got := displayDuplicates(t, store, target.ID); got != 0 {
		t.Errorf("duplicate issue numbers after merge = %d, want 0", got)
	}
	var counter, max int64
	if err := store.DB().QueryRowContext(ctx,
		`SELECT p.issue_counter, MAX(i.issue_number) FROM projects p JOIN issues i ON i.project_id = p.id WHERE p.id = ?`,
		target.ID).Scan(&counter, &max); err != nil {
		t.Fatal(err)
	}
	if counter != 5 || max != 5 {
		t.Errorf("counter = %d, highest number = %d, want 5 and 5", counter, max)
	}
}

// Duplicates from an earlier merge: the issues of the given environment get new
// numbers; the others keep theirs.
func TestRenumberDuplicateIssues(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	p, _ := store.CreateProject(ctx, "P", "proj")
	persistFingerprints(t, store, p.ID, "production", "a", "b", "c")
	persistFingerprints(t, store, p.ID, "e2e", "x", "y")
	// Recreate the collision: the e2e issues take numbers 1 and 2.
	mustExec(ctx, t, store, `UPDATE issues SET issue_number = 1 WHERE fingerprint = 'x'`)
	mustExec(ctx, t, store, `UPDATE issues SET issue_number = 2 WHERE fingerprint = 'y'`)

	changed, err := store.RenumberDuplicateIssues(ctx, "proj", "e2e")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 || displayDuplicates(t, store, p.ID) != 0 {
		t.Fatalf("changed = %v, duplicates = %d, want 2 renumbered and none left", changed, displayDuplicates(t, store, p.ID))
	}
	var native int64
	if err := store.DB().QueryRowContext(ctx, `SELECT issue_number FROM issues WHERE fingerprint = 'a'`).Scan(&native); err != nil || native != 1 {
		t.Errorf("native issue number = %d (%v), want 1", native, err)
	}
	again, err := store.RenumberDuplicateIssues(ctx, "proj", "e2e")
	if err != nil || len(again) != 0 {
		t.Errorf("second run = %v, %v, want nothing to do", again, err)
	}
	if _, err := store.RenumberDuplicateIssues(ctx, "missing", "e2e"); err == nil {
		t.Error("unknown project returned no error")
	}
}

// When every issue of a colliding pair carries the environment, the older row
// keeps the number.
func TestRenumberDuplicateIssuesKeepsOneWhenAllMatch(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	p, _ := store.CreateProject(ctx, "P", "proj")
	persistFingerprints(t, store, p.ID, "e2e", "a", "b")
	mustExec(ctx, t, store, `UPDATE issues SET issue_number = 1`)

	changed, err := store.RenumberDuplicateIssues(ctx, "proj", "e2e")
	if err != nil || len(changed) != 1 {
		t.Fatalf("changed = %v, %v, want one", changed, err)
	}
	var lowest int64
	if err := store.DB().QueryRowContext(ctx, `SELECT issue_number FROM issues WHERE fingerprint = 'a'`).Scan(&lowest); err != nil || lowest != 1 {
		t.Errorf("first issue number = %d (%v), want 1", lowest, err)
	}
}
