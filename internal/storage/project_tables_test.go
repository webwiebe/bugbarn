package storage

import (
	"testing"
)

// A table with a project_id column that projectTables does not list is skipped
// by DeleteProject and MergeProjects (bugbarn#270). Adding such a table in a
// migration fails here until it gets a registry entry.
func TestProjectTablesCoverEverySchemaTable(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)

	rows, err := store.DB().QueryContext(ctx,
		`SELECT m.name FROM sqlite_master m, pragma_table_info(m.name) p
		 WHERE m.type = 'table' AND p.name = 'project_id'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	inSchema := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		inSchema[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	inRegistry := map[string]bool{}
	for _, tbl := range projectTables {
		if inRegistry[tbl.name] {
			t.Errorf("projectTables lists %s twice", tbl.name)
		}
		inRegistry[tbl.name] = true
		if !inSchema[tbl.name] {
			t.Errorf("projectTables lists %s, which has no project_id column in the schema", tbl.name)
		}
	}
	for name := range inSchema {
		if !inRegistry[name] {
			t.Errorf("table %s has a project_id column and is missing from projectTables", name)
		}
	}
}

// Rows that would collide with the target's own are left with the source and go
// with it; the merge itself must not fail on them.
func TestMergeProjectsSkipsCollidingRows(t *testing.T) {
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
	for _, id := range []int64{source.ID, target.ID} {
		mustExec(ctx, t, store, `INSERT INTO settings (project_id, key, value) VALUES (?, 'k', ?)`, id, "v")
		mustExec(ctx, t, store,
			`INSERT INTO monitors (project_id, slug, interval_s, created_at) VALUES (?, 'nightly', 60, '2026-01-01T00:00:00Z')`, id)
	}
	mustExec(ctx, t, store,
		`INSERT INTO monitors (project_id, slug, interval_s, created_at) VALUES (?, 'hourly', 60, '2026-01-01T00:00:00Z')`, source.ID)

	if err := store.MergeProjects(ctx, "source-proj", "target-proj"); err != nil {
		t.Fatalf("MergeProjects with colliding rows: %v", err)
	}
	if got := countWhere(ctx, t, store, "monitors", target.ID); got != 2 {
		t.Errorf("target monitors = %d, want 2 (its own nightly plus the source's hourly)", got)
	}
	if got := countWhere(ctx, t, store, "settings", target.ID); got != 1 {
		t.Errorf("target settings = %d, want 1 (the target keeps its own)", got)
	}
}

func TestDeleteProjectRemovesEveryOwnedTable(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	p, err := store.CreateProject(ctx, "Doomed", "doomed")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, store,
		`INSERT INTO api_keys (name, project_id, key_sha256, scope, created_at) VALUES ('ci', ?, 'sha-doomed', 'ingest', '2026-01-01T00:00:00Z')`, p.ID)
	mustExec(ctx, t, store,
		`INSERT INTO releases (project_id, name, observed_at) VALUES (?, 'v1', '2026-01-01T00:00:00Z')`, p.ID)
	mustExec(ctx, t, store,
		`INSERT INTO monitors (project_id, slug, interval_s, created_at) VALUES (?, 'nightly', 60, '2026-01-01T00:00:00Z')`, p.ID)

	if err := store.DeleteProject(ctx, "doomed"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range projectTables {
		if got := countWhere(ctx, t, store, tbl.name, p.ID); got != 0 {
			t.Errorf("%s still has %d rows for the deleted project", tbl.name, got)
		}
	}
}
