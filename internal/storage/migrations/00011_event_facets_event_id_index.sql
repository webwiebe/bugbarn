-- +goose Up
-- event_facets.event_id carries ON DELETE CASCADE from events(id) but had no
-- index, so deleting one event forced a full scan of event_facets to find its
-- children. That made bulk deletion quadratic and effectively barred any event
-- retention policy: trimming 3M events would have scanned a ~10M-row table 3M
-- times.
--
-- Measured on a 20k-event / 60k-facet database, deleting one 2000-row batch:
--   with this index:     26ms
--   without this index:  11.1s
-- a 427x difference on a facets table orders of magnitude smaller than
-- production's. Without the index a single retention batch would hold the one
-- shared write connection for minutes and stall ingest behind it.
--
-- This is deliberately narrow (one integer column) rather than a re-add of
-- anything dropped by the 00008/00009 index diet: those indexes were dropped
-- because no query used their leading columns, whereas this one is required by
-- the foreign key itself on every delete. The pages it costs are far outweighed
-- by the rows retention can now reclaim.
CREATE INDEX IF NOT EXISTS idx_event_facets_event ON event_facets(event_id);

-- +goose Down
DROP INDEX IF EXISTS idx_event_facets_event;
