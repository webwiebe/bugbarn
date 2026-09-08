package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestFacetProjectionExcluded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		key   string
		value string
		want  bool
	}{
		{"environment is a facet", "attributes.environment", "production", false},
		{"host is a facet", "resource.host.name", "web-01", false},
		{"severity is a facet", "severity", "ERROR", false},
		{"trace id identifies one event", "traceId", "4bf92f3577b34da6a3ce929d0e0e4736", true},
		{"span id identifies one event", "spanId", "00f067aa0ba902b7", true},
		{"message varies per event", "message", "timeout talking to 10.0.0.4", true},
		{"the rendered exception is a payload", "exception", "{TypeError undefined is not a function []}", true},
		{"stack frames are a payload", "exception.stacktrace[0].function", "handleRequest", true},
		{"an overlong value is a payload", "attributes.body", strings.Repeat("x", maxFacetValueLength+1), true},
		{"a value at the limit still fits", "attributes.body", strings.Repeat("x", maxFacetValueLength), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := facetProjectionExcluded(tc.key, tc.value); got != tc.want {
				t.Errorf("facetProjectionExcluded(%q, ...) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// The projection's whole claim: facet storage grows with an issue's distinct
// facet values, not with its event count. event_facets grew with the event
// count — 2.25M rows for 260k events on production — which is what made it the
// single largest object in the database.
func TestIssueFacetsDoNotGrowWithEventCount(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	persist := func(i int) {
		t.Helper()
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, processedEventFrom(event.Event{
			ObservedAt: time.Date(2026, 9, 8, 12, 0, i, 0, time.UTC),
			ReceivedAt: time.Date(2026, 9, 8, 12, 0, i, 0, time.UTC),
			Severity:   "ERROR",
			Message:    "projection test error",
			// Unique per event, exactly like a real trace id.
			TraceID:    fmt.Sprintf("trace-%d", i),
			SpanID:     fmt.Sprintf("span-%d", i),
			Exception:  event.Exception{Type: "ProjectionError", Message: "projection test error"},
			Resource:   map[string]any{"host.name": "web-01"},
			Attributes: map[string]any{"environment": "production"},
		})); err != nil {
			t.Fatalf("persist event %d: %v", i, err)
		}
	}

	persist(0)
	after1 := countRows(t, store, "issue_facets")
	if after1 == 0 {
		t.Fatal("the first event of an issue wrote no facets at all")
	}

	for i := 1; i < 25; i++ {
		persist(i)
	}
	if got := countRows(t, store, "issue_facets"); got != after1 {
		t.Errorf("facet rows = %d after 25 events, want %d — the projection is still per event", got, after1)
	}

	for _, key := range []string{"traceId", "spanId", "message"} {
		var n int
		if err := store.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM issue_facets WHERE facet_key = ?`, key).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%q was projected %d times; per-event keys must not be facets", key, n)
		}
	}

	// The facets that survive are the ones the UI filters on.
	values, err := store.ListFacetValues(ctx, store.DefaultProjectID(), "attributes.environment")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "production" {
		t.Errorf("attributes.environment values = %v, want [production]", values)
	}
}

// Migration 00013 has to carry the existing per-event rows over, because a
// project's facet keys are what the environment switcher is built from and they
// would otherwise only reappear as new events arrive.
func TestMigration00013BackfillsProjectionFromEventFacets(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "migrate.db")
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

	// Stop one version short of the projection, on the schema production was
	// running when this change landed.
	if _, err := provider.UpTo(ctx, 12); err != nil {
		t.Fatalf("migrate to 00012: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO projects (slug, name) VALUES ('legacy', 'Legacy')`); err != nil {
		t.Fatal(err)
	}
	var projectID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug='legacy'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO issues (project_id, fingerprint, fingerprint_material, title, normalized_title,
			exception_type, first_seen, last_seen, event_count, representative_event_json, issue_number)
		VALUES (?, 'fp', '', 'issue', 'issue', 'Error', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 3, '{}', 1)`,
		projectID); err != nil {
		t.Fatal(err)
	}
	var issueID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM issues WHERE project_id = ?`, projectID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}

	// Three events carrying the same environment and a trace id each: the shape
	// that made event_facets grow one row per event.
	for i := 0; i < 3; i++ {
		res, err := db.ExecContext(ctx, `
			INSERT INTO events (project_id, issue_id, fingerprint, received_at, observed_at, severity, message, event_json)
			VALUES (?, ?, 'fp', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'error', 'test', '{}')`, projectID, issueID)
		if err != nil {
			t.Fatal(err)
		}
		eventID, _ := res.LastInsertId()
		rows := [][2]string{
			{"attributes.environment", "production"},
			{"traceId", fmt.Sprintf("trace-%d", i)},
			{"exception", strings.Repeat("frame ", 60)},
		}
		for _, kv := range rows {
			if _, err := db.ExecContext(ctx, `
				INSERT INTO event_facets (project_id, event_id, issue_id, section, facet_key, facet_value)
				VALUES (?, ?, ?, 'attributes', ?, ?)`,
				projectID, eventID, issueID, kv[0], kv[1]); err != nil {
				t.Fatal(err)
			}
		}
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	type facet struct{ key, value string }
	var got []facet
	rows, err := db.QueryContext(ctx,
		`SELECT facet_key, facet_value FROM issue_facets WHERE issue_id = ? ORDER BY facet_key`, issueID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var f facet
		if err := rows.Scan(&f.key, &f.value); err != nil {
			t.Fatal(err)
		}
		got = append(got, f)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := []facet{{"attributes.environment", "production"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("projected facets = %v, want %v (three events collapse to one row; per-event keys are dropped)", got, want)
	}
}
