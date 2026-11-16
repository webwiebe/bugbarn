package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// seedIssue inserts an issue with n events and returns its id.
func seedIssue(ctx context.Context, t *testing.T, store *Store, projectID int64, fingerprint string, number, events int, status string) int64 {
	t.Helper()
	res, err := store.DB().ExecContext(ctx, `
INSERT INTO issues (project_id, fingerprint, title, normalized_title, exception_type, status, issue_number,
                    first_seen, last_seen, event_count, representative_event_json)
VALUES (?, ?, 't', 't', 'E', ?, ?, '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', ?, '{}')`,
		projectID, fingerprint, status, number, events)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	for i := 0; i < events; i++ {
		mustExec(ctx, t, store, `
INSERT INTO events (project_id, issue_id, fingerprint, received_at, observed_at, severity, message, event_json)
VALUES (?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'error', 'm', '{}')`, projectID, id, fingerprint)
	}
	return id
}

func facetValues(ctx context.Context, t *testing.T, store *Store, issueID int64, key string) []string {
	t.Helper()
	rows, err := store.DB().QueryContext(ctx,
		`SELECT facet_value FROM issue_facets WHERE issue_id = ? AND facet_key = ? ORDER BY facet_value`, issueID, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestMergeFoldsCollidingIssuesAndRenumbersTheRest(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	source, _ := store.CreateProject(ctx, "Src", "shop-src")
	target, _ := store.CreateProject(ctx, "Shop", "shop")
	mustExec(ctx, t, store, `UPDATE projects SET issue_counter = 5 WHERE id = ?`, target.ID)
	mustExec(ctx, t, store, `UPDATE projects SET issue_counter = 2 WHERE id = ?`, source.ID)

	prodIssue := seedIssue(ctx, t, store, target.ID, "fp-shared", 4, 10, "resolved")
	mustExec(ctx, t, store, `INSERT INTO issue_facets (project_id, issue_id, facet_key, facet_value) VALUES (?, ?, 'environment', 'production')`, target.ID, prodIssue)
	stagingShared := seedIssue(ctx, t, store, source.ID, "fp-shared", 1, 3, "unresolved")
	stagingOnly := seedIssue(ctx, t, store, source.ID, "fp-only", 2, 2, "unresolved")
	mustExec(ctx, t, store, `INSERT INTO issue_facets (project_id, issue_id, facet_key, facet_value) VALUES (?, ?, 'environment', 'qa')`, source.ID, stagingOnly)

	res, err := store.MergeProjects(ctx, "shop-src", "shop", "staging")
	if err != nil {
		t.Fatal(err)
	}

	if res.Counts.IssuesFolded != 1 || res.Counts.IssuesMoved != 1 || res.Counts.Events != 5 {
		t.Errorf("counts = %+v, want 1 folded, 1 moved, 5 events", res.Counts)
	}
	var count int
	var status string
	if err := store.DB().QueryRowContext(ctx, `SELECT event_count, status FROM issues WHERE id = ?`, prodIssue).Scan(&count, &status); err != nil {
		t.Fatal(err)
	}
	if count != 13 || status != "unresolved" {
		t.Errorf("folded issue = %d events, %q; want 13, unresolved (a live failure in the source reopens it)", count, status)
	}
	if got := countWhere(ctx, t, store, "events", target.ID); got != 15 {
		t.Errorf("target events = %d, want 15", got)
	}
	var left int
	_ = store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE id = ?`, stagingShared).Scan(&left)
	if left != 0 {
		t.Error("the folded source issue still exists")
	}
	var number int
	var project int64
	if err := store.DB().QueryRowContext(ctx, `SELECT issue_number, project_id FROM issues WHERE id = ?`, stagingOnly).Scan(&number, &project); err != nil {
		t.Fatal(err)
	}
	if number != 7 || project != target.ID {
		t.Errorf("moved issue = number %d project %d, want 7 in target %d (offset 5)", number, project, target.ID)
	}
	got, _ := store.ProjectBySlug(ctx, "shop")
	if got.IssueCounter != 7 {
		t.Errorf("target issue counter = %d, want 7", got.IssueCounter)
	}

	// The environment follows the data: stamped where missing, kept where present.
	if v := facetValues(ctx, t, store, stagingOnly, "environment"); len(v) != 1 || v[0] != "qa" {
		t.Errorf("moved issue environment = %v, want its own [qa]", v)
	}
	if v := facetValues(ctx, t, store, prodIssue, "environment"); len(v) != 2 || v[0] != "production" || v[1] != "staging" {
		t.Errorf("folded issue environments = %v, want [production staging]", v)
	}
}

func TestMergeRecordsAuditAndRefusesAnArchivedSource(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	_, _ = store.CreateProject(ctx, "Src", "app-src")
	_, _ = store.CreateProject(ctx, "App", "app")
	mustExec(ctx, t, store, `INSERT INTO api_keys (name, project_id, key_sha256, scope, created_at)
SELECT 'k', id, 'sha-app-src', 'ingest', '2026-01-01T00:00:00Z' FROM projects WHERE slug = 'app-src'`)

	res, err := store.MergeProjects(ctx, "app-src", "app", "testing")
	if err != nil {
		t.Fatal(err)
	}
	if res.ID == 0 || res.Counts.APIKeys != 1 {
		t.Errorf("result = %+v, want an audit id and 1 key redirected", res)
	}
	merges, err := store.ProjectMerges(ctx)
	if err != nil || len(merges) != 1 || merges[0].Source != "app-src" || merges[0].Environment != "testing" {
		t.Fatalf("recorded merges = %+v, %v", merges, err)
	}

	if _, err := store.MergeProjects(ctx, "app-src", "app", "testing"); !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("merging an archived source = %v, want conflict", err)
	}
	if _, err := store.MergeProjects(ctx, "app", "app", "testing"); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("merging a project into itself = %v, want invalid input", err)
	}
	if _, err := store.MergeProjects(ctx, "app", "app-src", "Not An Env!"); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("bad environment name = %v, want invalid input", err)
	}
}

func TestCreateRefusesEnvironmentSuffixedNames(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	for _, slug := range []string{"shop-staging", "shop-test", "shop-prod", "shop_e2e", "Shop-Production"} {
		if _, err := store.CreateProject(ctx, "x", slug); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("CreateProject(%q) = %v, want invalid input", slug, err)
		}
		if _, err := store.CreateProjectPending(ctx, slug); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("CreateProjectPending(%q) = %v, want invalid input", slug, err)
		}
	}
	if _, err := store.CreateProject(ctx, "Read Only Test", "ro-proj"); err != nil {
		t.Errorf("a display name that ends in a word is fine: %v", err)
	}
}
