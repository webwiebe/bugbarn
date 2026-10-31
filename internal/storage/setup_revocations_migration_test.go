package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	fixtureProjects = 200
	fixtureKeys     = 3 // per project: a setup key row, an ingest key, a full key
)

// seedKeyFixtures fills the tables the migration sits next to with synthetic rows.
func seedKeyFixtures(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	for p := 1; p <= fixtureProjects; p++ {
		slug := fmt.Sprintf("fixture-%d", p)
		res, err := db.ExecContext(ctx, `INSERT INTO projects (slug, name) VALUES (?, ?)`, slug, slug)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		for k, row := range [][2]string{{slug + "-setup", "ingest"}, {slug + "-ci", "ingest"}, {slug + "-admin", "full"}} {
			if _, err := db.ExecContext(ctx, `
INSERT INTO api_keys (name, project_id, key_sha256, scope, created_at) VALUES (?, ?, ?, ?, '2026-01-01T00:00:00Z')`,
				row[0], id, fmt.Sprintf("hash-%d-%d", p, k), row[1]); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func countFixtureRows(ctx context.Context, t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Migration 00025 only adds its table: every key and project stays as it was, no
// key counts as revoked, and going back down removes the table and nothing else.
func TestMigrationAddsRevocationsAndLeavesKeysAlone(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 24); err != nil {
		t.Fatalf("migrate to 00024: %v", err)
	}
	seedKeyFixtures(ctx, t, db)
	wantProjects := countFixtureRows(ctx, t, db, "projects")
	wantKeys := countFixtureRows(ctx, t, db, "api_keys")
	if wantKeys < fixtureProjects*fixtureKeys {
		t.Fatalf("fixture keys = %d", wantKeys)
	}

	if _, err := provider.UpTo(ctx, 25); err != nil {
		t.Fatalf("migrate to 00025: %v", err)
	}
	if got := countFixtureRows(ctx, t, db, "projects"); got != wantProjects {
		t.Fatalf("projects after 00025 = %d, want %d", got, wantProjects)
	}
	if got := countFixtureRows(ctx, t, db, "api_keys"); got != wantKeys {
		t.Fatalf("api_keys after 00025 = %d, want %d", got, wantKeys)
	}
	if got := countFixtureRows(ctx, t, db, "setup_key_revocations"); got != 0 {
		t.Fatalf("revocations after 00025 = %d, want 0", got)
	}

	if _, err := provider.DownTo(ctx, 24); err != nil {
		t.Fatalf("migrate down to 00024: %v", err)
	}
	if got := countFixtureRows(ctx, t, db, "api_keys"); got != wantKeys {
		t.Fatalf("api_keys after going down = %d, want %d", got, wantKeys)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM setup_key_revocations`).Scan(new(int)); err == nil {
		t.Fatal("the table is still there after going down")
	}
	if _, err := provider.UpTo(ctx, 25); err != nil {
		t.Fatalf("migrate back up to 00025: %v", err)
	}

	// The store opens on the migrated file and revokes one fixture key alone.
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if revoked, err := store.SetupKeyRevoked(ctx, "fixture-1"); err != nil || revoked {
		t.Fatalf("a fixture key must start out live: revoked=%v err=%v", revoked, err)
	}
	if err := store.RevokeSetupKey(ctx, "fixture-1", "hash-1-0"); err != nil {
		t.Fatal(err)
	}
	if got := countFixtureRows(ctx, t, db, "api_keys"); got != wantKeys-1 {
		t.Fatalf("api_keys after one revocation = %d, want %d", got, wantKeys-1)
	}
	if revoked, err := store.SetupKeyRevoked(ctx, "fixture-2"); err != nil || revoked {
		t.Fatalf("another project's key must stay live: revoked=%v err=%v", revoked, err)
	}
}
