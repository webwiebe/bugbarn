package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// settingsTables lists the tables that make up a "settings-only" snapshot: the
// configuration an operator must not lose (project registry, users, ingest
// credentials, alert rules, org settings) as opposed to the high-volume data
// (events, issues, logs, analytics) that a disaster-recovery reset accepts
// losing.
//
// Order matters — foreign keys are enforced on the destination:
//   - projects references project_groups(id), so groups go first.
//   - project_aliases, api_keys, alerts and settings all reference projects(id).
//   - users is independent.
//
// Deliberately excluded:
//   - events, event_facets, issues, log_entries, analytics_*, alert_firings,
//     held_events, regression_events — the bulk data this snapshot trades away.
//   - releases and source_maps — both hang off a timeline of events that will
//     not exist after a reset, and CI re-posts a release marker and re-uploads
//     source maps on the next deploy.
//   - web_sessions — ephemeral auth sessions, worthless after a restore and not
//     worth writing live session tokens into a backup object.
var settingsTables = []string{
	"project_groups",
	"projects",
	"users",
	"project_aliases",
	"api_keys",
	"alerts",
	"settings",
}

// SnapshotSettings builds a fresh, ready-to-serve SQLite database at destPath
// containing only the settings tables copied from srcPath. Every other table is
// present (created by the normal migration path) but empty. The result can be
// dropped straight in as BUGBARN_DB_PATH: no further restore or migration step
// is needed.
//
// The source is only ever read: it is opened mode=ro and ATTACHed through a
// mode=ro URI, so this can be pointed at a live production database — including
// via a read-only volume mount — without any chance of writing to it.
//
// Returns per-table row counts copied into the snapshot, so the caller can
// report them as a sanity check.
func SnapshotSettings(ctx context.Context, srcPath, destPath string) (map[string]int64, error) {
	srcAbs, err := filepath.Abs(srcPath)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	if err := verifySource(ctx, srcAbs); err != nil {
		return nil, err
	}
	if err := resetSnapshotFile(destPath); err != nil {
		return nil, err
	}

	// autoMigrate=false: the background fingerprint migration is pointless here
	// (the snapshot carries no events) and would race the copy.
	dest, err := open(destPath, false)
	if err != nil {
		return nil, fmt.Errorf("create snapshot database: %w", err)
	}
	defer func() { _ = dest.Close() }()

	attachURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(srcAbs)}).String() + "?mode=ro"
	if _, err := dest.db.ExecContext(ctx, `ATTACH DATABASE ? AS src`, attachURI); err != nil {
		return nil, fmt.Errorf("attach source database: %w", err)
	}
	detached := false
	detach := func() {
		if !detached {
			_, _ = dest.db.ExecContext(context.Background(), `DETACH DATABASE src`)
			detached = true
		}
	}
	defer detach()

	counts, err := copySettingsTables(ctx, dest)
	if err != nil {
		return nil, err
	}

	// Detach before checkpointing: the source is attached read-only and must not
	// be touched by the checkpoint below.
	detach()

	// wal_autocheckpoint(0) means Close would leave the copied rows sitting in
	// the snapshot's WAL; fold them into the main file so the object we upload
	// is a single self-contained database.
	dest.FinalCheckpoint(nil)
	return counts, nil
}

// verifySource checks the source database exists and is readable before we
// create anything at the destination.
func verifySource(ctx context.Context, srcAbs string) error {
	if _, err := os.Stat(srcAbs); err != nil {
		return fmt.Errorf("source database %s: %w", srcAbs, err)
	}
	src, err := OpenReadOnly(srcAbs)
	if err != nil {
		return fmt.Errorf("open source database: %w", err)
	}
	defer func() { _ = src.Close() }()
	if err := src.readDB().PingContext(ctx); err != nil {
		return fmt.Errorf("ping source database: %w", err)
	}
	return nil
}

// resetSnapshotFile clears any previous snapshot. A stale -wal/-shm left beside
// a removed database would otherwise be adopted by the new file.
func resetSnapshotFile(destPath string) error {
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing snapshot at %s: %w", destPath, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(destPath + suffix)
	}
	return nil
}

// sharedColumns returns the columns table has in BOTH the snapshot (main) and
// the attached source (src), in the snapshot's declared order. Copying only the
// intersection keeps the snapshot working across a schema drift in either
// direction: a column the source predates simply takes its default, and one the
// source still carries but the schema has dropped is ignored.
func sharedColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	read := func(schema string) (map[string]bool, []string, error) {
		rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA %s.table_info(%s)`, schema, table))
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()
		set := map[string]bool{}
		var order []string
		for rows.Next() {
			var cid int
			var name, ctype string
			var notNull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
				return nil, nil, err
			}
			set[name] = true
			order = append(order, name)
		}
		return set, order, rows.Err()
	}

	_, destOrder, err := read("main")
	if err != nil {
		return nil, err
	}
	srcSet, _, err := read("src")
	if err != nil {
		return nil, err
	}
	var shared []string
	for _, c := range destOrder {
		if srcSet[c] {
			shared = append(shared, c)
		}
	}
	return shared, nil
}

// copySettingsTables clears the destination's settings tables and copies each
// one from the ATTACHed source, returning per-table row counts.
func copySettingsTables(ctx context.Context, dest *Store) (map[string]int64, error) {
	// A fresh database is not empty: open -> init seeds baseline rows (notably
	// the Default Project at id=1) and those collide with the source's own rows
	// on copy. Clear the settings tables first, children before parents, so the
	// snapshot ends up an exact mirror of the source rather than a merge.
	for i := len(settingsTables) - 1; i >= 0; i-- {
		if _, err := dest.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM main.%s`, settingsTables[i])); err != nil {
			return nil, fmt.Errorf("clear table %s: %w", settingsTables[i], err)
		}
	}

	counts := make(map[string]int64, len(settingsTables))
	for _, table := range settingsTables {
		// Map columns by NAME, never `SELECT *`. A long-lived database orders its
		// columns by how they were added (ALTER TABLE ADD COLUMN appends), while a
		// freshly-migrated one orders them as the CREATE TABLE declares. Those two
		// orders drift, and `SELECT *` then silently shifts values into the wrong
		// columns — which showed up as "NOT NULL constraint failed:
		// projects.created_at" when a NULL group_id landed in created_at.
		cols, err := sharedColumns(ctx, dest.db, table)
		if err != nil {
			return nil, fmt.Errorf("resolve columns for %s: %w", table, err)
		}
		if len(cols) == 0 {
			return nil, fmt.Errorf("table %s: no columns common to source and snapshot", table)
		}
		list := strings.Join(cols, ", ")
		// Table and column names come from the fixed settingsTables list and the
		// live schema, never user input, so interpolating them is safe.
		stmt := fmt.Sprintf(`INSERT INTO main.%s (%s) SELECT %s FROM src.%s`, table, list, list, table)
		if _, err := dest.db.ExecContext(ctx, stmt); err != nil {
			return nil, fmt.Errorf("copy table %s: %w", table, err)
		}
		var count int64
		if err := dest.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM main.%s`, table)).Scan(&count); err != nil {
			return nil, fmt.Errorf("count table %s: %w", table, err)
		}
		counts[table] = count
	}
	return counts, nil
}
