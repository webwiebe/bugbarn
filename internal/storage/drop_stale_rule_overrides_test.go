package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 00024 removes the stored overrides of disk-full and load-high so the
// built-ins take over, and leaves every other row alone.
func TestMigrationDropsStaleRuleOverrides(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "rules.db")))
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
	if _, err := provider.UpTo(ctx, 23); err != nil {
		t.Fatalf("migrate to 00023: %v", err)
	}
	for _, id := range []string{"disk-full", "load-high", "memory-low", "custom"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO detection_rules (id, rule_json) VALUES (?, ?)`, id, `{"id":"`+id+`"}`); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := provider.UpTo(ctx, 24); err != nil {
		t.Fatalf("migrate to 00024: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT id FROM detection_rules ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "custom" || got[1] != "memory-low" {
		t.Fatalf("rows after 00024 = %v, want [custom memory-low]", got)
	}
}
