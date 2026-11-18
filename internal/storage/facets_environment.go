package storage

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Facet keys that carry an event's environment. SDKs send the `environment`
// attribute, which the facet projection stores as attributes.environment (the
// key the dashboard filters on); the OpenTelemetry resource attribute
// deployment.environment is stored as environment.
const (
	EnvironmentFacetKey          = "environment"
	AttributeEnvironmentFacetKey = "attributes.environment"
)

// maxEnvironments bounds the list; a project has a handful of environments.
const maxEnvironments = 20

const environmentActivitySQL = `
SELECT env, COUNT(1), SUM(is_open), MAX(last_seen)
FROM (
	SELECT f.facet_value AS env, i.id AS id, i.last_seen AS last_seen,
		i.status IN ('unresolved', 'regressed') AS is_open
	FROM issue_facets f
	JOIN issues i ON i.id = f.issue_id AND i.project_id = f.project_id
	WHERE f.project_id = ? AND f.facet_key IN (?, ?)
	GROUP BY f.facet_value, i.id
)
GROUP BY env
ORDER BY MAX(last_seen) DESC, env ASC
LIMIT ?`

// EnvironmentActivity lists, per environment value, how many issues carry it,
// how many are open and the newest last_seen among them. An issue that carries
// the value under both facet keys counts once. Each facet side is a range on the
// issue_facets primary key (project, key, value, issue); each hit joins issues
// by primary key.
func (s *FacetStore) EnvironmentActivity(ctx context.Context, projectID int64) ([]domain.EnvironmentActivity, error) {
	rows, err := s.readDB().QueryContext(ctx, environmentActivitySQL,
		projectID, EnvironmentFacetKey, AttributeEnvironmentFacetKey, maxEnvironments)
	if err != nil {
		return nil, wrapErr(err, "environment activity")
	}
	defer rows.Close()

	var out []domain.EnvironmentActivity
	for rows.Next() {
		var r domain.EnvironmentActivity
		if err := rows.Scan(&r.Environment, &r.Issues, &r.Open, &r.LastSeen); err != nil {
			return nil, wrapErr(err, "scan environment activity")
		}
		out = append(out, r)
	}
	return out, wrapErr(rows.Err(), "environment activity rows")
}
