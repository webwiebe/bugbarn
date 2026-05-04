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
