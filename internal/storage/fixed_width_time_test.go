package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// SQLite compares timestamp columns as text, so formatTime must give an earlier
// time a smaller string, also inside one second (#253). RFC3339Nano trims the
// fraction's zeros and "Z" sorts after every digit and after ".", so both
// pairs below sorted the wrong way round.
func TestFormatTimeSortsInsideOneSecond(t *testing.T) {
	t.Parallel()
	sec := time.Date(2026, 11, 6, 1, 0, 55, 0, time.UTC)
	cases := []struct {
		name           string
		earlier, later time.Time
	}{
		{".12 before .123", sec.Add(120 * time.Millisecond), sec.Add(123 * time.Millisecond)},
		{"whole second before .003", sec, sec.Add(3 * time.Millisecond)},
	}
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "cmp.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, c := range cases {
		a, b := formatTime(c.earlier), formatTime(c.later)
		if a >= b {
			t.Errorf("%s: formatTime gives %q >= %q", c.name, a, b)
		}
		var less bool
		if err := db.QueryRow(`SELECT ? < ?`, a, b).Scan(&less); err != nil {
			t.Fatal(err)
		}
		if !less {
			t.Errorf("%s: SQLite sorts %q after %q", c.name, a, b)
		}
		if len(a) != len(b) {
			t.Errorf("%s: widths differ: %q, %q", c.name, a, b)
		}
	}
}

// Rows written before #253 keep the trimmed form; parseTime still reads them,
// and the parsed time formats to the same instant in the new form.
func TestParseTimeReadsTrimmedAndFixedForms(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 11, 6, 1, 0, 55, 120_000_000, time.UTC)
	for _, s := range []string{"2026-11-06T01:00:55.12Z", "2026-11-06T01:00:55.120000000Z"} {
		got, err := parseTime(s)
		if err != nil || !got.Equal(want) {
			t.Fatalf("parseTime(%q) = %v, %v", s, got, err)
		}
	}
	if got := formatTime(want); got != "2026-11-06T01:00:55.120000000Z" {
		t.Fatalf("formatTime = %q", got)
	}
	whole, err := parseTime("2026-11-06T01:00:55Z")
	if err != nil || !whole.Equal(want.Truncate(time.Second)) {
		t.Fatalf("parseTime whole second = %v, %v", whole, err)
	}
}

// An old trimmed value and a new fixed-width value in different seconds compare
// correctly in both directions: they differ before the fraction starts. Inside
// one second an old value keeps the error it had before #253, at most a second.
func TestMixedFormsSortAcrossSeconds(t *testing.T) {
	t.Parallel()
	old := []string{"2026-11-06T01:00:55Z", "2026-11-06T01:00:55.12Z", "2026-11-06T01:00:55.999Z"}
	before := formatTime(time.Date(2026, 11, 6, 1, 0, 54, 999_999_999, time.UTC))
	after := formatTime(time.Date(2026, 11, 6, 1, 0, 56, 0, time.UTC))
	for _, o := range old {
		if before >= o || o >= after {
			t.Errorf("%q does not sort between %q and %q", o, before, after)
		}
	}
}

// Migration 00026 rewrites trimmed last_checkin_at values to the fixed-width
// form, so MarkMonitorMissed, which matches the column with "=", still finds a
// monitor whose last check-in came before the change.
func TestMigrationRewritesTrimmedCheckIns(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "monitors.db")
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
	if _, err := provider.UpTo(ctx, 25); err != nil {
		t.Fatalf("migrate to 00025: %v", err)
	}
	res, err := db.ExecContext(ctx, `INSERT INTO projects (slug, name) VALUES ('geo', 'geo')`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct{ slug, stored, want string }{
		{"whole", "2026-11-06T01:00:55Z", "2026-11-06T01:00:55.000000000Z"},
		{"trimmed", "2026-11-06T01:00:55.12Z", "2026-11-06T01:00:55.120000000Z"},
		{"one-digit", "2026-11-06T01:00:55.5Z", "2026-11-06T01:00:55.500000000Z"},
		{"full", "2026-11-06T01:00:55.123456789Z", "2026-11-06T01:00:55.123456789Z"},
		{"never", "", ""},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `
INSERT INTO monitors (project_id, slug, interval_s, created_at, last_checkin_at)
VALUES (?, ?, 3600, '2026-11-01T00:00:00Z', ?)`, projectID, r.slug, r.stored); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.UpTo(ctx, 26); err != nil {
		t.Fatalf("migrate to 00026: %v", err)
	}
	for _, r := range rows {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT last_checkin_at FROM monitors WHERE slug = ?`, r.slug).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != r.want {
			t.Errorf("%s: last_checkin_at = %q, want %q", r.slug, got, r.want)
		}
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM monitors WHERE slug = 'trimmed'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, 11, 6, 1, 0, 55, 120_000_000, time.UTC)
	applied, err := store.MarkMonitorMissed(ctx, id, seen, seen.Add(2*time.Hour))
	if err != nil || !applied {
		t.Fatalf("miss on a pre-change check-in: applied=%v err=%v", applied, err)
	}
}
