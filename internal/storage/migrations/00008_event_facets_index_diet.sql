-- +goose Up
-- event_facets carried 4 indexes over ~2.25M rows (a large share of the DB).
-- Two of them are dead weight — no query in the codebase uses their leading
-- columns:
--   - idx_event_facets_lookup leads with `section`, which no query ever filters
--     on (the column is written but never read by a WHERE clause).
--   - idx_event_facets_issue leads with issue_id, which no query ever filters on
--     (issue_id only ever appears as a SELECTed result column).
-- Every per-project access pattern — the ingest write-path cardinality/existence
-- checks and the UI's per-project facet-value listing and "filter issues by
-- facet" — is already served by idx_event_facets_kv_issue
-- (project_id, facet_key, facet_value, issue_id), so dropping these two changes
-- no behaviour and removes no capability.
--
-- The freed pages go to the freelist; run VACUUM during a quiet window to shrink
-- the file on disk.
DROP INDEX IF EXISTS idx_event_facets_lookup;
DROP INDEX IF EXISTS idx_event_facets_issue;

-- +goose Down
CREATE INDEX IF NOT EXISTS idx_event_facets_lookup ON event_facets(project_id, section, facet_key, facet_value);
CREATE INDEX IF NOT EXISTS idx_event_facets_issue ON event_facets(project_id, issue_id, facet_key, facet_value);
