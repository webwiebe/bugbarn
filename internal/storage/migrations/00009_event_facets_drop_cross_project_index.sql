-- +goose Up
-- Drop idx_event_facets_facet (facet_key, facet_value, issue_id). It served ONLY
-- cross-project (projectID=0) facet queries: list-all-keys, list-all-values, and
-- filter-issues-by-facet across every project. Nothing reachable issues those —
-- the facets endpoint always resolves a concrete project, and the all-projects /
-- group UI scopes deliberately carry no facet filter. The cross-project facet
-- code paths are removed in the same change (ListFacetKeys/ListFacetValues and
-- buildIssueFromClause are now always project-scoped), so a stray query can't
-- silently fall back to a full scan of ~2.25M rows. Removing this 3-column index
-- reclaims a meaningful chunk of the DB.
DROP INDEX IF EXISTS idx_event_facets_facet;

-- +goose Down
CREATE INDEX IF NOT EXISTS idx_event_facets_facet ON event_facets(facet_key, facet_value, issue_id);
