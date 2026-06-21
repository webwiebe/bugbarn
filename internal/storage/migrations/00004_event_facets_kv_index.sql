-- +goose Up
-- The facet existence checks in storage.PersistFacets filter on
-- (project_id, facet_key, facet_value) without section or issue_id. The two
-- existing indexes lead with (project_id, section, ...) and
-- (project_id, issue_id, ...), so neither can serve those predicates beyond the
-- project_id prefix — every check scanned the project's entire event_facets
-- partition. As the table grew this made each ingested event O(n) in the
-- project's facet count, the single SQLite writer connection was held for
-- minutes per event, and the write pipeline wedged (production outage,
-- 2026-06-21). This covering index turns those COUNT() checks into point
-- lookups. IF NOT EXISTS so it is a no-op where the index was already created
-- as an emergency hotfix.
CREATE INDEX IF NOT EXISTS idx_event_facets_kv ON event_facets(project_id, facet_key, facet_value);

-- +goose Down
DROP INDEX IF EXISTS idx_event_facets_kv;
