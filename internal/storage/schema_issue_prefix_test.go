package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// migrateIssuePrefixes gives each project without a prefix one derived from its
// slug (a numeric suffix on a collision), numbers its unnumbered issues 1..n in
// id order, and sets the project's counter to n.
func TestMigrateIssuePrefixesAssignsPrefixesAndNumbers(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	db := store.DB()

	addProject := func(slug, prefix string) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx,
			`INSERT INTO projects (slug, name, status, issue_prefix) VALUES (?, ?, 'active', ?)`, slug, slug, prefix)
		if err != nil {
			t.Fatalf("insert project %s: %v", slug, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	addIssues := func(projectID int64, n int) []int64 {
		t.Helper()
		var ids []int64
		for i := 0; i < n; i++ {
			res, err := db.ExecContext(ctx, `
				INSERT INTO issues (project_id, fingerprint, fingerprint_material, title, normalized_title,
					exception_type, first_seen, last_seen, event_count, representative_event_json, issue_number)
				VALUES (?, ?, '', 'issue', 'issue', 'Error', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1, '{}', 0)`,
				projectID, fmt.Sprintf("fp-%d-%d", projectID, i))
			if err != nil {
				t.Fatalf("insert issue: %v", err)
			}
			id, _ := res.LastInsertId()
			ids = append(ids, id)
		}
		return ids
	}

	taken := addProject("zed", "ALP") // already owns ALP, so the next alp* project must get a suffix
	alpha := addProject("alpha", "")
	alphabet := addProject("alphabet", "")
	alphaIssues := addIssues(alpha, 3)
	alphabetIssues := addIssues(alphabet, 1)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateIssuePrefixes(ctx, tx); err != nil {
		t.Fatalf("migrateIssuePrefixes: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	prefixOf := func(id int64) (string, int) {
		var prefix string
		var counter int
		if err := db.QueryRowContext(ctx, `SELECT issue_prefix, issue_counter FROM projects WHERE id = ?`, id).Scan(&prefix, &counter); err != nil {
			t.Fatal(err)
		}
		return prefix, counter
	}
	if p, _ := prefixOf(taken); p != "ALP" {
		t.Fatalf("existing prefix changed to %q", p)
	}
	p1, c1 := prefixOf(alpha)
	p2, c2 := prefixOf(alphabet)
	if p1 != "ALP2" || c1 != 3 {
		t.Fatalf("alpha = %q counter %d, want ALP2 and 3", p1, c1)
	}
	if p2 != "ALP3" || c2 != 1 {
		t.Fatalf("alphabet = %q counter %d, want ALP3 and 1", p2, c2)
	}
	for i, id := range alphaIssues {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT issue_number FROM issues WHERE id = ?`, id).Scan(&n); err != nil || n != i+1 {
			t.Fatalf("alpha issue %d number = %d, %v, want %d", id, n, err, i+1)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT issue_number FROM issues WHERE id = ?`, alphabetIssues[0]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("alphabet issue number = %d, %v, want 1", n, err)
	}
}
