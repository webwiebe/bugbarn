package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func planOf(t *testing.T, store *Store, query string, args ...any) string {
	t.Helper()
	rows, err := store.readDB().Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, " | ")
}

// The silence signal reads stay on their indexes: the event reads are answered
// from idx_events_project_received_issue alone, the log read walks
// idx_log_entries_project_id, and nothing scans a whole table.
func TestSilenceSignalQueryPlans(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	cases := []struct {
		name, query, want string
		args              []any
	}{
		{"newest event", newestEventSQL,
			"COVERING INDEX idx_events_project_received_issue", []any{1, 1}},
		{"event times", eventTimesSQL,
			"COVERING INDEX idx_events_project_received_issue", []any{1, "2026-01-01", 1, 10}},
		{"newest log row", newestLogSQL,
			"idx_log_entries_project_id", []any{1}},
	}
	for _, tc := range cases {
		plan := planOf(t, store, tc.query, tc.args...)
		if !strings.Contains(plan, tc.want) {
			t.Errorf("%s: plan %q does not use %s", tc.name, plan, tc.want)
		}
		if strings.Contains(plan, "SCAN events") || strings.Contains(plan, "SCAN log_entries") {
			t.Errorf("%s: plan %q scans a whole table", tc.name, plan)
		}
	}
}

func TestSilenceSignalsEmptyProject(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	p := store.Domains().Projects

	if _, ok, err := p.NewestLogAt(ctx, 99); err != nil || ok {
		t.Fatalf("NewestLogAt on an empty project = ok %v err %v", ok, err)
	}
	if _, ok, err := p.NewestEventAt(ctx, 99); err != nil || ok {
		t.Fatalf("NewestEventAt on an empty project = ok %v err %v", ok, err)
	}
	if _, ok, err := p.NewestKeyUse(ctx, 99); err != nil || ok {
		t.Fatalf("NewestKeyUse on an empty project = ok %v err %v", ok, err)
	}
	if got, err := p.EventTimesSince(ctx, 99, time.Now().Add(-time.Hour), 10); err != nil || len(got) != 0 {
		t.Fatalf("EventTimesSince = %v err %v", got, err)
	}
}
