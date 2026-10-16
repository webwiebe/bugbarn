package storage

import (
	"context"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// SurfaceFacetKey is the flattened facet key of the `surface` event attribute.
const SurfaceFacetKey = "attributes.surface"

// maxSurfaces bounds the panel; a project has a handful of surfaces.
const maxSurfaces = 20

// SurfaceActivity counts, per value of the surface attribute, the issues last
// seen within 24 hours and within 7 days, and how many of those are open. The
// facet side is a range on the issue_facets primary key (project, key, value,
// issue); each hit joins issues by primary key.
func (s *FacetStore) SurfaceActivity(ctx context.Context, projectID int64, now time.Time) ([]domain.SurfaceActivity, error) {
	day := formatTime(now.Add(-24 * time.Hour))
	week := formatTime(now.Add(-7 * 24 * time.Hour))
	rows, err := s.readDB().QueryContext(ctx, `
SELECT f.facet_value,
	SUM(i.last_seen >= ?),
	COUNT(1),
	SUM(i.status IN ('unresolved', 'regressed'))
FROM issue_facets f
JOIN issues i ON i.id = f.issue_id AND i.project_id = f.project_id
WHERE f.project_id = ? AND f.facet_key = ? AND i.last_seen >= ?
GROUP BY f.facet_value
ORDER BY COUNT(1) DESC, f.facet_value ASC
LIMIT ?`,
		day, projectID, SurfaceFacetKey, week, maxSurfaces)
	if err != nil {
		return nil, wrapErr(err, "surface activity")
	}
	defer rows.Close()

	var out []domain.SurfaceActivity
	for rows.Next() {
		var r domain.SurfaceActivity
		if err := rows.Scan(&r.Surface, &r.Issues1d, &r.Issues7d, &r.Open); err != nil {
			return nil, wrapErr(err, "scan surface activity")
		}
		out = append(out, r)
	}
	return out, wrapErr(rows.Err(), "surface activity rows")
}
