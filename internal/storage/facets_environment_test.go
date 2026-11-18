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

	// key is the attribute that carries the environment: the resource-style
	// deployment.environment or the SDK's plain environment attribute.
	persistKey := func(fingerprint, key, env string, at time.Time) {
		t.Helper()
		attrs := map[string]any{}
		if env != "" {
			attrs[key] = env
		}
		p := releaseEvent(at, attrs, nil)
		p.Fingerprint = fingerprint
		p.FingerprintMaterial = fingerprint
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, p); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
	persist := func(fingerprint, env string, at time.Time) {
		persistKey(fingerprint, "deployment.environment", env, at)
	}
	persist("prod-a", "production", now.Add(-time.Hour))
	persist("prod-b", "production", now.Add(-48*time.Hour))
	persist("stg-a", "staging", now.Add(-10*time.Minute))
	persist("none", "", now.Add(-time.Minute))
	// The Go SDK sends a plain environment attribute; it counts with the rest.
	persistKey("sdk-prod", "environment", "production", now.Add(-2*time.Hour))
	persistKey("sdk-dev", "environment", "development", now.Add(-20*time.Minute))

	got, err := store.EnvironmentActivity(ctx, store.defaultProjectID)
	if err != nil {
		t.Fatalf("EnvironmentActivity: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want staging, development and production only, got %+v", got)
	}
	if got[0].Environment != "staging" || got[0].Issues != 1 || got[0].Open != 1 || got[0].LastSeen == "" {
		t.Errorf("staging row: %+v", got[0])
	}
	if got[1].Environment != "development" || got[1].Issues != 1 {
		t.Errorf("development row: %+v", got[1])
	}
	if got[2].Environment != "production" || got[2].Issues != 3 || got[2].Open != 3 {
		t.Errorf("production row: %+v", got[2])
	}
	if got[0].LastSeen <= got[1].LastSeen || got[1].LastSeen <= got[2].LastSeen {
		t.Errorf("rows must be newest first: %+v", got)
	}
}

func TestEnvironmentActivityUsesFacetPrimaryKey(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	rows, err := store.readDB().Query("EXPLAIN QUERY PLAN "+environmentActivitySQL, 1, EnvironmentFacetKey, AttributeEnvironmentFacetKey, 20)
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
	if !strings.Contains(joined, "SEARCH f USING PRIMARY KEY") || strings.Contains(joined, "SCAN f") || strings.Contains(joined, "SCAN i") {
		t.Fatalf("facet side must be a keyed search, plan: %s", joined)
	}
}
