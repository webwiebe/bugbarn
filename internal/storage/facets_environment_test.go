package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentActivityListsEnvironmentsNewestFirst(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	persist := func(fingerprint, env string, at time.Time) {
		t.Helper()
		attrs := map[string]any{}
		if env != "" {
			attrs["deployment.environment"] = env
		}
		p := releaseEvent(at, attrs, nil)
		p.Fingerprint = fingerprint
		p.FingerprintMaterial = fingerprint
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, p); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
	persist("prod-a", "production", now.Add(-time.Hour))
	persist("prod-b", "production", now.Add(-48*time.Hour))
	persist("stg-a", "staging", now.Add(-10*time.Minute))
	persist("none", "", now.Add(-time.Minute))

	got, err := store.EnvironmentActivity(ctx, store.defaultProjectID)
	if err != nil {
		t.Fatalf("EnvironmentActivity: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want staging and production only, got %+v", got)
	}
	if got[0].Environment != "staging" || got[0].Issues != 1 || got[0].Open != 1 || got[0].LastSeen == "" {
		t.Errorf("staging row: %+v", got[0])
	}
	if got[1].Environment != "production" || got[1].Issues != 2 || got[1].Open != 2 {
		t.Errorf("production row: %+v", got[1])
	}
	if got[0].LastSeen <= got[1].LastSeen {
		t.Errorf("rows must be newest first: %q then %q", got[0].LastSeen, got[1].LastSeen)
	}
}

func TestEnvironmentActivityUsesFacetPrimaryKey(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	rows, err := store.readDB().Query(`EXPLAIN QUERY PLAN
SELECT f.facet_value, COUNT(1), SUM(i.status IN ('unresolved', 'regressed')), MAX(i.last_seen)
FROM issue_facets f JOIN issues i ON i.id = f.issue_id AND i.project_id = f.project_id
WHERE f.project_id = ? AND f.facet_key = ?
GROUP BY f.facet_value`, 1, EnvironmentFacetKey)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, "SEARCH f USING PRIMARY KEY") || strings.Contains(joined, "SCAN") {
		t.Fatalf("facet side must be a keyed search, plan: %s", joined)
	}
}
