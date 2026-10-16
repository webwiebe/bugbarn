package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func surfaceEvent(at time.Time, message, surface string) (attrs map[string]any) {
	attrs = map[string]any{"release": "1.0.0"}
	if surface != "" {
		attrs["surface"] = surface
	}
	return attrs
}

func TestSurfaceActivityGroupsIssuesBySurface(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	persist := func(fingerprint, surface string, at time.Time) {
		t.Helper()
		p := releaseEvent(at, surfaceEvent(at, fingerprint, surface), nil)
		p.Fingerprint = fingerprint
		p.FingerprintMaterial = fingerprint
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, p); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
	persist("wp-a", "wordpress", now.Add(-time.Hour))
	persist("wp-b", "wordpress", now.Add(-3*24*time.Hour))
	persist("lr-a", "lightroom", now.Add(-2*time.Hour))
	persist("wp-old", "wordpress", now.Add(-30*24*time.Hour))
	persist("none", "", now.Add(-time.Hour))

	got, err := store.SurfaceActivity(ctx, store.defaultProjectID, now)
	if err != nil {
		t.Fatalf("SurfaceActivity: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want wordpress and lightroom only, got %+v", got)
	}
	if got[0].Surface != "wordpress" || got[0].Issues7d != 2 || got[0].Issues1d != 1 || got[0].Open != 2 {
		t.Errorf("wordpress row: %+v", got[0])
	}
	if got[1].Surface != "lightroom" || got[1].Issues7d != 1 || got[1].Issues1d != 1 {
		t.Errorf("lightroom row: %+v", got[1])
	}
}

func TestSurfaceActivityUsesFacetPrimaryKey(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	rows, err := store.readDB().Query(`EXPLAIN QUERY PLAN
SELECT f.facet_value, SUM(i.last_seen >= ?), COUNT(1), SUM(i.status IN ('unresolved', 'regressed'))
FROM issue_facets f JOIN issues i ON i.id = f.issue_id AND i.project_id = f.project_id
WHERE f.project_id = ? AND f.facet_key = ? AND i.last_seen >= ?
GROUP BY f.facet_value`, "a", 1, SurfaceFacetKey, "b")
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
