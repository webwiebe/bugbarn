package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

// The per-project usage aggregate has to stay index-only.
//
// It sums sample_weight across the whole events table. COUNT(*) could be
// answered from any project_id index; SUM of a column that is in no index
// cannot, and every row then costs a table lookup that pulls a page holding a
// multi-kilobyte event_json. That is exactly what shipped in v0.236.181:
// GET /api/v1/projects?usage=true went from 0.05s to 30s on production and hit
// the request timeout, so every project's counts rendered as "—".
//
// A plan assertion rather than a timing assertion, because the failure is
// structural — the index stops covering the query — and timings on a shared
// build agent prove nothing.
func TestProjectUsageAggregateUsesCoveringIndex(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const query = `SELECT project_id, SUM(sample_weight) cnt FROM events GROUP BY project_id`
	plan := queryPlan(t, store, "EXPLAIN QUERY PLAN "+query)

	if !strings.Contains(plan, "COVERING INDEX") {
		t.Fatalf("the usage aggregate reads the table instead of an index — on production that is a "+
			"page fetch per event row and a 30s timeout.\nquery: %s\nplan:  %s", query, plan)
	}
	if !strings.Contains(plan, "idx_events_project_sample_weight") {
		t.Errorf("expected the aggregate to use idx_events_project_sample_weight.\nplan: %s", plan)
	}
}
