-- +goose Up
-- Facet read/filter paths beyond the PersistFacets existence checks were
-- under-indexed:
--
--   * issues.go ListIssuesFilteredByFacets, project-scoped:
--       SELECT DISTINCT issue_id ... WHERE project_id=? AND facet_key=? AND facet_value=?
--     idx_event_facets_kv covered the WHERE but not issue_id, so each matching
--     row needed a table lookup. Widen it to a covering index (issue_id last).
--     The new index is a superset of idx_event_facets_kv, which is therefore
--     redundant and dropped — keeping the per-insert index count flat.
--
--   * The cross-project ("all projects") variants filter on facet_key /
--     facet_value with no project_id (ListFacetKeys, ListFacetValues, and the
--     facet issue filter). Every existing index leads with project_id, so those
--     queries full-scanned event_facets. Add a facet-leading index to serve them.
DROP INDEX IF EXISTS idx_event_facets_kv;
CREATE INDEX IF NOT EXISTS idx_event_facets_kv_issue
  ON event_facets(project_id, facet_key, facet_value, issue_id);
CREATE INDEX IF NOT EXISTS idx_event_facets_facet
  ON event_facets(facet_key, facet_value, issue_id);

-- +goose Down
DROP INDEX IF EXISTS idx_event_facets_facet;
DROP INDEX IF EXISTS idx_event_facets_kv_issue;
CREATE INDEX IF NOT EXISTS idx_event_facets_kv ON event_facets(project_id, facet_key, facet_value);
