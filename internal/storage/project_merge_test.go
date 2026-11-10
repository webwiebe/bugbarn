package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func openMergeStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, context.Background()
}

func mustExec(ctx context.Context, t *testing.T, store *Store, query string, args ...any) {
	t.Helper()
	if _, err := store.DB().ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func countWhere(ctx context.Context, t *testing.T, store *Store, table string, projectID int64) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+table+" WHERE project_id = ?", projectID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// bugbarn#270 (H38): api_keys.project_id has no ON DELETE action, so the final
// DELETE of the source project failed while the source still had a key.
func TestMergeProjectsMovesAPIKeysAndReleases(t *testing.T) {
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
	mustExec(ctx, t, store,
		`INSERT INTO api_keys (name, project_id, key_sha256, scope, created_at) VALUES ('ci', ?, 'sha-source', 'ingest', '2026-01-01T00:00:00Z')`, source.ID)
	mustExec(ctx, t, store,
		`INSERT INTO releases (project_id, name, observed_at) VALUES (?, 'v1', '2026-01-01T00:00:00Z')`, source.ID)

	if err := store.MergeProjects(ctx, "source-proj", "target-proj"); err != nil {
		t.Fatalf("MergeProjects with an API key on the source: %v", err)
	}

	if got := countWhere(ctx, t, store, "api_keys", target.ID); got != 1 {
		t.Errorf("target api_keys = %d, want 1 (the source key follows the merge)", got)
	}
	if got := countWhere(ctx, t, store, "releases", target.ID); got != 1 {
		t.Errorf("target releases = %d, want 1", got)
	}
	if got := countWhere(ctx, t, store, "api_keys", source.ID); got != 0 {
		t.Errorf("source api_keys = %d, want 0", got)
	}
}
