package storage

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	if _, err := os.Stat(srcAbs); err != nil {
		return nil, fmt.Errorf("source database %s: %w", srcAbs, err)
	}

	// Start from a clean destination; a stale -wal/-shm alongside a removed db
	// would otherwise be adopted by the new file.
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove existing snapshot at %s: %w", destPath, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(destPath + suffix)
	}

	// Verify the source is readable before creating anything.
	src, err := OpenReadOnly(srcAbs)
	if err != nil {
		return nil, fmt.Errorf("open source database: %w", err)
	}
	if err := src.readDB().PingContext(ctx); err != nil {
		src.Close()
		return nil, fmt.Errorf("ping source database: %w", err)
	}
	src.Close()

	// autoMigrate=false: the background fingerprint migration is pointless here
	// (the snapshot carries no events) and would race the copy.
	dest, err := open(destPath, false)
	if err != nil {
		return nil, fmt.Errorf("create snapshot database: %w", err)
	}
	defer dest.Close()

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
		// Table names are from the fixed settingsTables list above, never user
		// input, so interpolating them is safe.
		if _, err := dest.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO main.%s SELECT * FROM src.%s`, table, table)); err != nil {
			return nil, fmt.Errorf("copy table %s: %w", table, err)
		}
		var count int64
		if err := dest.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM main.%s`, table)).Scan(&count); err != nil {
			return nil, fmt.Errorf("count table %s: %w", table, err)
		}
		counts[table] = count
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
