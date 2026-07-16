package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// seedSnapshotSource builds a source database holding both settings rows and
// bulk rows, and deliberately leaves it open (and therefore in WAL mode with a
// live -wal file) so the snapshot is exercised against a live database rather
// than a cleanly-closed one — which is how the CronJob will really run.
func seedSnapshotSource(t *testing.T, path string) *Store {
	t.Helper()
	src, err := Open(path)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	ctx := context.Background()
	db := src.db

	if _, err := db.ExecContext(ctx, `INSERT INTO project_groups (name, slug) VALUES ('Group A', 'group-a')`); err != nil {
		t.Fatalf("seed project_groups: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO projects (slug, name, status, issue_prefix, group_id) VALUES ('web', 'Web', 'active', 'WEB', 1)`); err != nil {
		t.Fatalf("seed projects: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (username, password_bcrypt, created_at, updated_at)
		 VALUES ('admin', 'x', '2026-01-01', '2026-01-01')`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// Bulk data that must NOT survive into the snapshot.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO issues (project_id, fingerprint, title, normalized_title, exception_type,
		                     first_seen, last_seen, event_count, representative_event_json)
		 VALUES (1, 'fp1', 'boom', 'boom', 'Error', '2026-01-01', '2026-01-01', 1, '{}')`); err != nil {
		t.Fatalf("seed issues: %v", err)
	}
	return src
}

func TestSnapshotSettingsCopiesSettingsAndOmitsBulk(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "bugbarn.db")
	destPath := filepath.Join(dir, "settings.db")

	src := seedSnapshotSource(t, srcPath)
	defer src.Close() // stays open: snapshot must work against a live WAL database.

	counts, err := SnapshotSettings(context.Background(), srcPath, destPath)
	if err != nil {
		t.Fatalf("SnapshotSettings: %v", err)
	}

	// Compare against the source rather than a literal: open() seeds a Default
	// Project, so the source holds that plus the seeded 'web' project.
	var srcProjects int64
	if err := src.db.QueryRow(`SELECT count(*) FROM projects`).Scan(&srcProjects); err != nil {
		t.Fatalf("count source projects: %v", err)
	}
	if counts["projects"] != srcProjects {
		t.Errorf("projects copied = %d, want %d (all source projects)", counts["projects"], srcProjects)
	}
	if counts["project_groups"] != 1 {
		t.Errorf("project_groups copied = %d, want 1", counts["project_groups"])
	}
	if counts["users"] != 1 {
		t.Errorf("users copied = %d, want 1", counts["users"])
	}

	// The snapshot must be directly openable as a working database.
	snap, err := Open(destPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()

	// The seeded project must survive by slug — not just by count — and must not
	// have been duplicated by the destination's own baseline seed.
	var webCount int
	if err := snap.db.QueryRow(`SELECT count(*) FROM projects WHERE slug = 'web'`).Scan(&webCount); err != nil {
		t.Fatalf("count snapshot 'web' project: %v", err)
	}
	if webCount != 1 {
		t.Errorf("snapshot 'web' projects = %d, want exactly 1", webCount)
	}
	var snapProjects int64
	if err := snap.db.QueryRow(`SELECT count(*) FROM projects`).Scan(&snapProjects); err != nil {
		t.Fatalf("count snapshot projects: %v", err)
	}
	if snapProjects != srcProjects {
		t.Errorf("snapshot projects = %d, want %d (exact mirror of source)", snapProjects, srcProjects)
	}

	// Bulk tables must exist but be empty — that is the whole trade.
	var issues int
	if err := snap.db.QueryRow(`SELECT count(*) FROM issues`).Scan(&issues); err != nil {
		t.Fatalf("count snapshot issues (table should exist): %v", err)
	}
	if issues != 0 {
		t.Errorf("snapshot issues = %d, want 0 (bulk data must not be copied)", issues)
	}
	var events int
	if err := snap.db.QueryRow(`SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatalf("count snapshot events (table should exist): %v", err)
	}
	if events != 0 {
		t.Errorf("snapshot events = %d, want 0", events)
	}
}

// The source must be untouched: the CronJob points this at the live production
// database, so a stray write would be a real incident.
func TestSnapshotSettingsDoesNotWriteSource(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "bugbarn.db")
	destPath := filepath.Join(dir, "settings.db")

	src := seedSnapshotSource(t, srcPath)
	src.FinalCheckpoint(nil)
	if err := src.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	before, err := os.Stat(srcPath)
	if err != nil {
		t.Fatalf("stat source: %v", err)
	}

	if _, err := SnapshotSettings(context.Background(), srcPath, destPath); err != nil {
		t.Fatalf("SnapshotSettings: %v", err)
	}

	after, err := os.Stat(srcPath)
	if err != nil {
		t.Fatalf("stat source after: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("source database was modified: size %d->%d, mtime %v->%v",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
}

// Production's schema orders columns by how they were added (ALTER TABLE ADD
// COLUMN appends), which drifts from a freshly-migrated database's CREATE TABLE
// order. `SELECT *` silently shifts values into the wrong columns when that
// happens — in production it surfaced as "NOT NULL constraint failed:
// projects.created_at" because a NULL group_id landed in created_at. The copy
// must map columns by name.
func TestSnapshotSettingsSurvivesColumnOrderDrift(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "bugbarn.db")
	destPath := filepath.Join(dir, "settings.db")

	src := seedSnapshotSource(t, srcPath)

	// Rebuild projects with group_id moved to the end, mimicking a column added
	// by a later ALTER TABLE, then close so the snapshot reads the drifted file.
	stmts := []string{
		`PRAGMA foreign_keys=OFF`,
		`CREATE TABLE projects_drifted (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slug TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			issue_prefix TEXT NOT NULL DEFAULT '',
			issue_counter INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			group_id INTEGER REFERENCES project_groups(id) ON DELETE SET NULL
		)`,
		`INSERT INTO projects_drifted (id, slug, name, status, issue_prefix, issue_counter, created_at, group_id)
		 SELECT id, slug, name, status, issue_prefix, issue_counter, created_at, group_id FROM projects`,
		`DROP TABLE projects`,
		`ALTER TABLE projects_drifted RENAME TO projects`,
	}
	for _, s := range stmts {
		if _, err := src.db.Exec(s); err != nil {
			t.Fatalf("drift setup (%.40s): %v", s, err)
		}
	}
	src.FinalCheckpoint(nil)
	if err := src.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	counts, err := SnapshotSettings(context.Background(), srcPath, destPath)
	if err != nil {
		t.Fatalf("SnapshotSettings against drifted column order: %v", err)
	}
	if counts["projects"] == 0 {
		t.Fatal("no projects copied from the drifted source")
	}

	snap, err := Open(destPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()

	// The values must land in the right columns, not merely copy without error.
	var slug, createdAt string
	if err := snap.db.QueryRow(
		`SELECT slug, created_at FROM projects WHERE slug = 'web'`,
	).Scan(&slug, &createdAt); err != nil {
		t.Fatalf("read snapshot project: %v", err)
	}
	if createdAt == "" {
		t.Error("created_at is empty in the snapshot — columns were mapped positionally")
	}
}

func TestSnapshotSettingsRejectsMissingSource(t *testing.T) {
	dir := t.TempDir()
	_, err := SnapshotSettings(context.Background(), filepath.Join(dir, "nope.db"), filepath.Join(dir, "out.db"))
	if err == nil {
		t.Fatal("expected an error for a missing source database, got nil")
	}
}
