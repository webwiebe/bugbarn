-- +goose Up
-- Replace the per-event facet table with a per-issue projection.
--
-- event_facets held one row per (event, key, value): 2.25M rows on production
-- against 260k events, and with its indexes the single largest contributor to a
-- 4.3GB database. Nothing ever read a facet by event. Every reader — the issue
-- list's facet filter, the facet key/value listings behind the environment
-- switcher — asks the same question: which issues in this project carry this
-- key/value. That question only needs the distinct set.
--
-- issue_facets stores exactly that set. It is WITHOUT ROWID and every column is
-- part of the primary key, so the table *is* its index: one B-tree instead of a
-- rowid table plus two secondary indexes. Column order matches the read
-- predicates (project_id, facet_key, facet_value) with issue_id last as the
-- projected column, so all three reads are covering index scans.
--
-- Two columns do not survive. `section` was written on every row and never read
-- by a WHERE clause (migration 00008 already dropped the index leading with it).
-- `event_id` existed only for the ON DELETE CASCADE, which is now gone by
-- design: facets belong to the issue and outlive the 30-day event retention
-- window, so retention no longer has to cascade millions of facet deletes and
-- idx_event_facets_event goes with the table it indexed.
--
-- The backfill applies the same projection rules as the write path
-- (facetProjectionExcluded in internal/storage/facets.go): keys that identify a
-- single event rather than describe a class of them are not facets. Projecting
-- them per issue would defeat the whole change, since an issue with 90k events
-- has 90k distinct trace ids.
CREATE TABLE IF NOT EXISTS issue_facets (
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    issue_id    INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    facet_key   TEXT NOT NULL,
    facet_value TEXT NOT NULL,
    PRIMARY KEY (project_id, facet_key, facet_value, issue_id)
) WITHOUT ROWID;

-- The projected columns are listed in the order of idx_event_facets_kv_issue
-- (project_id, facet_key, facet_value, issue_id) so the DISTINCT is an ordered
-- walk of that covering index rather than a 2.25M-row sort into a temporary
-- B-tree. This runs on the writer at startup while the startupProbe holds
-- liveness off, the same budget migration 00011 spent 7m17s of on production.
INSERT OR IGNORE INTO issue_facets (project_id, facet_key, facet_value, issue_id)
SELECT DISTINCT project_id, facet_key, facet_value, issue_id
FROM event_facets
WHERE facet_key NOT IN ('message', 'traceId', 'spanId', 'exception')
  AND facet_key NOT LIKE 'exception.stacktrace%'
  AND length(facet_value) <= 200;

DROP TABLE IF EXISTS event_facets;

-- +goose Down
-- The per-event rows cannot be reconstructed: the projection deliberately threw
-- away which event carried which facet, and the excluded keys are gone entirely.
-- Down restores the shape, not the data. Facets re-accumulate from ingest.
CREATE TABLE IF NOT EXISTS event_facets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    event_id    INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    issue_id    INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    section     TEXT NOT NULL,
    facet_key   TEXT NOT NULL,
    facet_value TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_event_facets_kv_issue ON event_facets(project_id, facet_key, facet_value, issue_id);
CREATE INDEX IF NOT EXISTS idx_event_facets_event ON event_facets(event_id);
DROP TABLE IF EXISTS issue_facets;
