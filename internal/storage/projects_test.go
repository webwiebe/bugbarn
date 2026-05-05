package storage

import (
	"context"
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
