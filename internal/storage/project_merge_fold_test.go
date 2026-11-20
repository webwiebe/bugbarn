package storage

import (
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// A source issue whose fingerprint the target already holds folds into the
// target issue instead of failing the merge on UNIQUE(project_id, fingerprint).
func TestMergeProjectsFoldsCollidingFingerprints(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	source, err := store.CreateProject(ctx, "Source", "source-proj")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateProject(ctx, "Target", "target-proj")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	persist := func(projectID int64, fingerprint string, attrs map[string]any) {
		t.Helper()
		p := releaseEvent(now, attrs, nil)
		p.Fingerprint = fingerprint
		p.FingerprintMaterial = fingerprint
		if _, _, _, _, err := store.PersistProcessedEvent(domain.WithProjectID(ctx, projectID), p); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
	persist(target.ID, "same", map[string]any{"environment": "production"})
	persist(source.ID, "same", map[string]any{})
	persist(source.ID, "same", map[string]any{})
	persist(source.ID, "only-source", map[string]any{})

	if err := store.MergeProjectsAs(ctx, "source-proj", "target-proj", "staging"); err != nil {
		t.Fatalf("MergeProjectsAs with a colliding fingerprint: %v", err)
	}

	var issues, folded, events int
	if err := store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(CASE WHEN fingerprint = 'same' THEN event_count END), 0) FROM issues WHERE project_id = ?`,
		target.ID).Scan(&issues, &folded); err != nil {
		t.Fatal(err)
	}
	if issues != 2 || folded != 3 {
		t.Errorf("target issues = %d, folded event_count = %d, want 2 and 3", issues, folded)
	}
	if err := store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events e JOIN issues i ON i.id = e.issue_id WHERE i.fingerprint = 'same' AND e.project_id = ?`,
		target.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 3 {
		t.Errorf("events on the folded issue = %d, want 3", events)
	}
	got, err := store.EnvironmentActivity(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, row := range got {
		counts[row.Environment] = row.Issues
	}
	if counts["production"] != 1 || counts["staging"] != 2 {
		t.Errorf("issue environments = %v, want production 1 and staging 2", counts)
	}
}

// A failure while folding surfaces as an error and the transaction rolls back.
func TestFoldCollidingIssuesReportsFailures(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	source, err := store.CreateProject(ctx, "Source", "source-proj")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateProject(ctx, "Target", "target-proj")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []int64{source.ID, target.ID} {
		p := releaseEvent(now, map[string]any{}, nil)
		p.Fingerprint, p.FingerprintMaterial = "same", "same"
		if _, _, _, _, err := store.PersistProcessedEvent(domain.WithProjectID(ctx, id), p); err != nil {
			t.Fatal(err)
		}
	}
	for _, drop := range []string{"regression_events", "issues"} {
		tx, err := store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+drop); err != nil {
			t.Fatal(err)
		}
		if err := foldCollidingIssues(ctx, tx, source.ID, target.ID); err == nil {
			t.Errorf("fold with %s dropped returned no error", drop)
		}
		_ = tx.Rollback()
	}
}
