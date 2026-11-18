package storage

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// EnvironmentFacetKey is the facet key of the event's environment.
const EnvironmentFacetKey = "environment"

// maxEnvironments bounds the list; a project has a handful of environments.
const maxEnvironments = 20

// EnvironmentActivity lists, per value of the environment facet, how many
// issues carry it, how many are open and the newest last_seen among them. The
// facet side is a range on the issue_facets primary key (project, key, value,
// issue); each hit joins issues by primary key.
func (s *FacetStore) EnvironmentActivity(ctx context.Context, projectID int64) ([]domain.EnvironmentActivity, error) {
	rows, err := s.readDB().QueryContext(ctx, `
SELECT f.facet_value,
	COUNT(1),
	SUM(i.status IN ('unresolved', 'regressed')),
	MAX(i.last_seen)
FROM issue_facets f
JOIN issues i ON i.id = f.issue_id AND i.project_id = f.project_id
WHERE f.project_id = ? AND f.facet_key = ?
GROUP BY f.facet_value
ORDER BY MAX(i.last_seen) DESC, f.facet_value ASC
LIMIT ?`,
		projectID, EnvironmentFacetKey, maxEnvironments)
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
