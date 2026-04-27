package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

const (
	maxFacetKeysPerProject = 50
	maxFacetValuesPerKey   = 10_000
)

// extractFacets pulls a fixed set of well-known fields from an event into a
// flat key→value map. Only non-empty string values are included.
func extractFacets(evt event.Event) map[string]string {
	out := make(map[string]string)

	resourceFields := []string{
		"host.name",
		"service.name",
		"telemetry.sdk.language",
		"deployment.environment",
	}
	for _, field := range resourceFields {
		if v := stringFromMap(evt.Resource, field); v != "" {
			out[field] = v
		}
	}

	attributeFields := []string{
		"http.route",
		"http.status_code",
		"http.method",
		"user_agent.original",
	}
	for _, field := range attributeFields {
		if v := stringFromMap(evt.Attributes, field); v != "" {
			out[field] = v
		}
	}

	if evt.Severity != "" {
		out["severity"] = evt.Severity
	}

	// environment: prefer attributes over resource
	if env := stringFromMap(evt.Attributes, "deployment.environment"); env != "" {
		out["environment"] = env
	} else if env := stringFromMap(evt.Resource, "deployment.environment"); env != "" {
		out["environment"] = env
	}

	if rel := stringFromMap(evt.Attributes, "release"); rel != "" {
		out["release"] = rel
	}

	return out
}

// stringFromMap retrieves a string value from a map[string]any. It first tries
// the key as a literal (e.g. "host.name" stored flat), then falls back to a
// nested lookup (e.g. map["host"]["name"]).
func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}

	// Helper to coerce a value to string.
	asString := func(v any) string {
		switch typed := v.(type) {
		case string:
			return typed
		case fmt.Stringer:
			return typed.String()
		case float64:
			if typed == float64(int64(typed)) {
				return fmt.Sprintf("%d", int64(typed))
			}
			return fmt.Sprintf("%g", typed)
		}
		return ""
	}

	// Try the literal key first (e.g. "host.name" → map["host.name"]).
	if v, ok := m[key]; ok {
		if s := asString(v); s != "" {
			return s
		}
	}

	// Fall back to nested lookup for dotted keys.
	parts := strings.SplitN(key, ".", 2)
	if len(parts) < 2 {
		return ""
	}
	sub, ok := m[parts[0]]
	if !ok {
		return ""
	}
	nested, ok := sub.(map[string]any)
	if !ok {
		return ""
	}
	return stringFromMap(nested, parts[1])
}

// PersistFacets inserts extracted facet key/value pairs for a given event into
// event_facets, enforcing per-project cardinality caps (T030).
func (s *Store) PersistFacets(ctx context.Context, eventID int64, issueID int64, facets map[string]string) error {
	if len(facets) == 0 {
		return nil
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Count existing distinct facet keys for this project once up-front.
	var keyCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT facet_key) FROM event_facets WHERE project_id = ?`,
		projectID,
	).Scan(&keyCount); err != nil {
		return err
	}

	for k, v := range facets {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}

		// Determine whether this key is new to this project.
		var existingKeyCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM event_facets WHERE project_id = ? AND facet_key = ?`,
			projectID, k,
		).Scan(&existingKeyCount); err != nil {
			return err
		}
		isNewKey := existingKeyCount == 0

		// Cardinality guard: max 50 distinct keys per project.
		if isNewKey && keyCount >= maxFacetKeysPerProject {
			continue
		}

		// Determine whether this specific value is new for this key.
		var valueExists int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM event_facets WHERE project_id = ? AND facet_key = ? AND facet_value = ?`,
			projectID, k, v,
		).Scan(&valueExists); err != nil {
			return err
		}
		isNewValue := valueExists == 0

		// Cardinality guard: max 10,000 distinct values per key.
		if isNewValue {
			var distinctValueCount int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(DISTINCT facet_value) FROM event_facets WHERE project_id = ? AND facet_key = ?`,
				projectID, k,
			).Scan(&distinctValueCount); err != nil {
				return err
			}
			if distinctValueCount >= maxFacetValuesPerKey {
				continue
			}
		}

		section := rootSection(k)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO event_facets (project_id, event_id, issue_id, section, facet_key, facet_value) VALUES (?, ?, ?, ?, ?, ?)`,
			projectID, eventID, issueID, section, k, v,
		); err != nil {
			return err
		}

		if isNewKey {
			keyCount++
		}
	}

	return tx.Commit()
}

// ListFacetKeys returns all distinct facet keys observed for a project.
// Pass projectID=0 to query across all projects.
func (s *Store) ListFacetKeys(ctx context.Context, projectID int64) ([]string, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if projectID != 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT DISTINCT facet_key FROM event_facets WHERE project_id = ? ORDER BY facet_key ASC`,
			projectID,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT DISTINCT facet_key FROM event_facets ORDER BY facet_key ASC`,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// ListFacetValues returns all distinct values observed for a facet key in a project.
// Pass projectID=0 to query across all projects.
func (s *Store) ListFacetValues(ctx context.Context, projectID int64, key string) ([]string, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if projectID != 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT DISTINCT facet_value FROM event_facets WHERE project_id = ? AND facet_key = ? ORDER BY facet_value ASC`,
			projectID, key,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT DISTINCT facet_value FROM event_facets WHERE facet_key = ? ORDER BY facet_value ASC`,
			key,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}
