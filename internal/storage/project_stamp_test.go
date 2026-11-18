package storage

import (
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// MergeProjectsAs gives the source rows that name no environment the given one
// and leaves the rows that already name one alone.
func TestMergeProjectsAsStampsEnvironment(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	source, err := store.CreateProject(ctx, "Source", "source-proj")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateProject(ctx, "Target", "target-proj")
	if err != nil {
		t.Fatal(err)
	}

	srcCtx := domain.WithProjectID(ctx, source.ID)
	now := time.Now().UTC()
	persist := func(fingerprint string, attrs map[string]any) {
		t.Helper()
		p := releaseEvent(now, attrs, nil)
		p.Fingerprint = fingerprint
		p.FingerprintMaterial = fingerprint
		if _, _, _, _, err := store.PersistProcessedEvent(srcCtx, p); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
	persist("bare", map[string]any{})
	persist("sdk", map[string]any{"environment": "staging"})
	persist("otel", map[string]any{"deployment.environment": "testing"})

	mustExec(ctx, t, store,
		`INSERT INTO releases (project_id, name, observed_at, environment) VALUES (?, 'v1', '2026-01-01T00:00:00Z', '')`, source.ID)
	mustExec(ctx, t, store,
		`INSERT INTO releases (project_id, name, observed_at, environment) VALUES (?, 'v2', '2026-01-02T00:00:00Z', 'staging')`, source.ID)
	mustExec(ctx, t, store,
		`INSERT INTO log_entries (project_id, received_at, message, data_json) VALUES (?, '2026-01-01T00:00:00Z', 'plain', '{}')`, source.ID)
	mustExec(ctx, t, store,
		`INSERT INTO log_entries (project_id, received_at, message, data_json) VALUES (?, '2026-01-01T00:00:01Z', 'tagged', '{"environment":"staging"}')`, source.ID)
	mustExec(ctx, t, store,
		`INSERT INTO log_entries (project_id, received_at, message, data_json) VALUES (?, '2026-01-01T00:00:02Z', 'broken', 'not json')`, source.ID)

	if err := store.MergeProjectsAs(ctx, "source-proj", "target-proj", "production"); err != nil {
		t.Fatalf("MergeProjectsAs: %v", err)
	}

	got, err := store.EnvironmentActivity(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, row := range got {
		counts[row.Environment] = row.Issues
	}
	if len(counts) != 3 || counts["production"] != 1 || counts["staging"] != 1 || counts["testing"] != 1 {
		t.Errorf("issue environments = %v, want production, staging and testing once each", counts)
	}

	envs := func(query string) map[string]string {
		rows, err := store.DB().QueryContext(ctx, query, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			out[k] = v
		}
		return out
	}
	rel := envs(`SELECT name, environment FROM releases WHERE project_id = ?`)
	if rel["v1"] != "production" || rel["v2"] != "staging" {
		t.Errorf("release environments = %v", rel)
	}
	logs := envs(`SELECT message, COALESCE(json_extract(CASE WHEN json_valid(data_json) THEN data_json ELSE '{}' END, '$.environment'), '') FROM log_entries WHERE project_id = ?`)
	if logs["plain"] != "production" || logs["tagged"] != "staging" || logs["broken"] != "" {
		t.Errorf("log environments = %v", logs)
	}
}

// Without an environment the merge stamps nothing.
func TestMergeProjectsAsWithoutEnvironmentStampsNothing(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	source, _ := store.CreateProject(ctx, "Source", "source-proj")
	target, _ := store.CreateProject(ctx, "Target", "target-proj")
	mustExec(ctx, t, store,
		`INSERT INTO releases (project_id, name, observed_at, environment) VALUES (?, 'v1', '2026-01-01T00:00:00Z', '')`, source.ID)

	if err := store.MergeProjectsAs(ctx, "source-proj", "target-proj", ""); err != nil {
		t.Fatal(err)
	}
	var env string
	if err := store.DB().QueryRowContext(ctx, `SELECT environment FROM releases WHERE project_id = ?`, target.ID).Scan(&env); err != nil {
		t.Fatal(err)
	}
	if env != "" {
		t.Errorf("environment = %q, want empty", env)
	}
}
