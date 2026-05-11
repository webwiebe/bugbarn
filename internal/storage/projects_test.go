package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestListProjectsSortedAlphabetically(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	names := []string{"Zebra", "Alpha", "Mango"}
	for _, name := range names {
		if _, err := store.CreateProject(ctx, name, name); err != nil {
			t.Fatalf("create project %q: %v", name, err)
		}
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Should include the seed "Default Project" plus the three we created.
	if got := len(projects); got != 4 {
		t.Fatalf("expected 4 projects, got %d", got)
	}

	want := []string{"Alpha", "Default Project", "Mango", "Zebra"}
	for i, w := range want {
		if projects[i].Name != w {
			t.Errorf("projects[%d].Name = %q, want %q", i, projects[i].Name, w)
		}
	}
}

func TestCreateProjectConflict(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	if _, err := store.CreateProject(ctx, "Dup", "dup"); err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateProject(ctx, "Dup Again", "dup")
	if err == nil {
		t.Fatal("expected error on duplicate slug")
	}
}

func TestAliasCreateAndResolve(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	p, err := store.CreateProject(ctx, "Original", "original")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.CreateAlias(ctx, "old-name", p.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.ResolveAlias(ctx, "old-name")
	if err != nil {
		t.Fatal(err)
	}
	if got != p.ID {
		t.Errorf("ResolveAlias = %d, want %d", got, p.ID)
	}

	// Delete alias
	if err := store.DeleteAlias(ctx, "old-name"); err != nil {
		t.Fatal(err)
	}
	_, err = store.ResolveAlias(ctx, "old-name")
	if err == nil {
		t.Fatal("expected error after alias deleted")
	}
}

func TestEnsureProjectResolvesAlias(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	p, err := store.CreateProject(ctx, "Target", "target-proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAlias(ctx, "alias-slug", p.ID); err != nil {
		t.Fatal(err)
	}

	// EnsureProject with the alias slug should return the target, not create new.
	got, err := store.EnsureProject(ctx, "alias-slug")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID {
		t.Errorf("EnsureProject returned ID %d, want %d", got.ID, p.ID)
	}
	if got.Slug != "target-proj" {
		t.Errorf("EnsureProject returned slug %q, want %q", got.Slug, "target-proj")
	}
}

func TestRenameProjectCreatesAlias(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	p, err := store.CreateProject(ctx, "OldName", "old-slug")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.RenameProject(ctx, "old-slug", "new-slug", "NewName"); err != nil {
		t.Fatal(err)
	}

	// New slug should work.
	renamed, err := store.ProjectBySlug(ctx, "new-slug")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != p.ID {
		t.Errorf("renamed project ID %d != original %d", renamed.ID, p.ID)
	}
	if renamed.Name != "NewName" {
		t.Errorf("renamed project name = %q, want %q", renamed.Name, "NewName")
	}

	// Old slug should resolve via alias.
	aliasID, err := store.ResolveAlias(ctx, "old-slug")
	if err != nil {
		t.Fatal(err)
	}
	if aliasID != p.ID {
		t.Errorf("alias resolves to %d, want %d", aliasID, p.ID)
	}
}

func TestMergeProjectsMovesDataAndCreatesAlias(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	source, err := store.CreateProject(ctx, "Source", "source-proj")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateProject(ctx, "Target", "target-proj")
	if err != nil {
		t.Fatal(err)
	}

	// Insert an alert for the source project.
	_, err = store.DB().ExecContext(ctx, `INSERT INTO alerts (project_id, name) VALUES (?, 'test-alert')`, source.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.MergeProjects(ctx, "source-proj", "target-proj"); err != nil {
		t.Fatal(err)
	}

	// Source project should no longer exist.
	_, err = store.ProjectBySlug(ctx, "source-proj")
	if err == nil {
		t.Fatal("expected source project to be deleted")
	}

	// Alias should resolve to target.
	aliasID, err := store.ResolveAlias(ctx, "source-proj")
	if err != nil {
		t.Fatal(err)
	}
	if aliasID != target.ID {
		t.Errorf("alias resolves to %d, want %d", aliasID, target.ID)
	}

	// Alert should now belong to target.
	var alertProjectID int64
	err = store.DB().QueryRowContext(ctx, `SELECT project_id FROM alerts WHERE name = 'test-alert'`).Scan(&alertProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if alertProjectID != target.ID {
		t.Errorf("alert project_id = %d, want %d", alertProjectID, target.ID)
	}
}

func TestDeleteProjectWithRelatedData(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	p, err := store.CreateProject(ctx, "LoadTest", "load-test")
	if err != nil {
		t.Fatal(err)
	}

	db := store.DB()

	// Insert issues, events, event_facets, releases, alerts, alert_firings,
	// settings, source_maps, api_keys, log_entries, project_aliases, analytics.
	for i := 0; i < 5; i++ {
		_, err := db.ExecContext(ctx, `INSERT INTO issues (project_id, fingerprint, fingerprint_material, title, normalized_title, exception_type, first_seen, last_seen, event_count, representative_event_json, issue_number) VALUES (?, ?, '', ?, ?, 'Error', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1, '{}', ?)`,
			p.ID, fmt.Sprintf("fp-%d", i), fmt.Sprintf("issue %d", i), fmt.Sprintf("issue %d", i), i+1)
		if err != nil {
			t.Fatalf("insert issue: %v", err)
		}
	}

	// Get issue IDs.
	rows, err := db.QueryContext(ctx, `SELECT id FROM issues WHERE project_id = ?`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var issueIDs []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		issueIDs = append(issueIDs, id)
	}
	rows.Close()

	for _, issueID := range issueIDs {
		for j := 0; j < 3; j++ {
			_, err := db.ExecContext(ctx, `INSERT INTO events (project_id, issue_id, fingerprint, received_at, observed_at, severity, message, event_json) VALUES (?, ?, 'fp', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'error', 'test', '{}')`,
				p.ID, issueID)
			if err != nil {
				t.Fatalf("insert event: %v", err)
			}
		}
	}

	// event_facets
	var eventID int64
	db.QueryRowContext(ctx, `SELECT id FROM events WHERE project_id = ? LIMIT 1`, p.ID).Scan(&eventID)
	_, err = db.ExecContext(ctx, `INSERT INTO event_facets (project_id, event_id, issue_id, section, facet_key, facet_value) VALUES (?, ?, ?, 'tags', 'env', 'prod')`,
		p.ID, eventID, issueIDs[0])
	if err != nil {
		t.Fatalf("insert facet: %v", err)
	}

	// releases
	_, err = db.ExecContext(ctx, `INSERT INTO releases (project_id, name, observed_at) VALUES (?, 'v1.0', CURRENT_TIMESTAMP)`, p.ID)
	if err != nil {
		t.Fatalf("insert release: %v", err)
	}

	// alerts + alert_firings
	res, err := db.ExecContext(ctx, `INSERT INTO alerts (project_id, name) VALUES (?, 'test-alert')`, p.ID)
	if err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	alertID, _ := res.LastInsertId()
	_, err = db.ExecContext(ctx, `INSERT INTO alert_firings (alert_id, issue_id) VALUES (?, ?)`, alertID, issueIDs[0])
	if err != nil {
		t.Fatalf("insert alert_firing: %v", err)
	}

	// settings
	_, err = db.ExecContext(ctx, `INSERT INTO settings (project_id, key, value) VALUES (?, 'foo', 'bar')`, p.ID)
	if err != nil {
		t.Fatalf("insert setting: %v", err)
	}

	// source_maps
	_, err = db.ExecContext(ctx, `INSERT INTO source_maps (project_id, release, bundle_url) VALUES (?, 'v1', 'app.js')`, p.ID)
	if err != nil {
		t.Fatalf("insert source_map: %v", err)
	}

	// api_keys
	_, err = db.ExecContext(ctx, `INSERT INTO api_keys (name, project_id, key_sha256, created_at) VALUES ('k1', ?, 'abc123', CURRENT_TIMESTAMP)`, p.ID)
	if err != nil {
		t.Fatalf("insert api_key: %v", err)
	}

	// log_entries
	_, err = db.ExecContext(ctx, `INSERT INTO log_entries (project_id, received_at, level, message) VALUES (?, CURRENT_TIMESTAMP, 'error', 'boom')`, p.ID)
	if err != nil {
		t.Fatalf("insert log_entry: %v", err)
	}

	// project_aliases
	_, err = db.ExecContext(ctx, `INSERT INTO project_aliases (alias_slug, project_id) VALUES ('old-load-test', ?)`, p.ID)
	if err != nil {
		t.Fatalf("insert alias: %v", err)
	}

	// analytics_pageviews
	_, err = db.ExecContext(ctx, `INSERT INTO analytics_pageviews (project_id, ts, pathname) VALUES (?, 1700000000, '/home')`, p.ID)
	if err != nil {
		t.Fatalf("insert pageview: %v", err)
	}

	// analytics_daily
	_, err = db.ExecContext(ctx, `INSERT INTO analytics_daily (project_id, date, pageviews, sessions) VALUES (?, '2024-01-01', 100, 50)`, p.ID)
	if err != nil {
		t.Fatalf("insert daily: %v", err)
	}

	// Now delete the project.
	if err := store.DeleteProject(ctx, "load-test"); err != nil {
		t.Fatalf("DeleteProject failed: %v", err)
	}

	// Verify project is gone.
	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE slug = 'load-test'`).Scan(&count)
	if count != 0 {
		t.Error("project still exists after delete")
	}

	// Verify cascade cleaned up child rows.
	for _, table := range []string{"issues", "events", "event_facets", "releases", "alerts", "settings", "source_maps", "api_keys", "log_entries", "project_aliases", "analytics_pageviews", "analytics_daily"} {
		db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE project_id = ?`, table), p.ID).Scan(&count)
		if count != 0 {
			t.Errorf("table %s still has %d rows for deleted project", table, count)
		}
	}
}

func TestGroupCRUD(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create group.
	g, err := store.CreateGroup(ctx, "Backend Services", "backend")
	if err != nil {
		t.Fatal(err)
	}
	if g.Slug != "backend" {
		t.Errorf("group slug = %q, want %q", g.Slug, "backend")
	}

	// List groups.
	groups, err := store.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	// Create a project and assign to group.
	p, err := store.CreateProject(ctx, "API", "api-svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AssignProjectToGroup(ctx, "api-svc", "backend"); err != nil {
		t.Fatal(err)
	}

	// List group projects.
	gps, err := store.ListGroupProjects(ctx, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if len(gps) != 1 || gps[0].ID != p.ID {
		t.Errorf("expected project %d in group, got %v", p.ID, gps)
	}

	// Remove project from group.
	if err := store.RemoveProjectFromGroup(ctx, "api-svc"); err != nil {
		t.Fatal(err)
	}
	gps, err = store.ListGroupProjects(ctx, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if len(gps) != 0 {
		t.Errorf("expected 0 projects in group after removal, got %d", len(gps))
	}

	// Delete group.
	if err := store.DeleteGroup(ctx, "backend"); err != nil {
		t.Fatal(err)
	}
	groups, err = store.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("expected 0 groups after deletion, got %d", len(groups))
	}
}
