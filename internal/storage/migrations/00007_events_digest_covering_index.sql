-- +goose Up
-- The weekly digest's top-issues query aggregates events per issue within a
-- 7-day window per project:
--   SELECT i.id, ..., COUNT(e.id) FROM events e JOIN issues i ON i.id = e.issue_id
--   WHERE e.project_id = ? AND e.received_at >= ? GROUP BY i.id ...
-- idx_events_project_received_at is (project_id, received_at, id) and does NOT
-- carry issue_id, so the GROUP BY forced a table rowid lookup for every event in
-- the window. For high-volume projects (tens of thousands of events/week) that
-- was tens of thousands of random lookups per project, run serially across the
-- whole fleet under one timeout — the digest's "context deadline exceeded".
-- Adding issue_id makes the scan covering: issue_id is read straight from the
-- index, so no per-event table access.
CREATE INDEX IF NOT EXISTS idx_events_project_received_issue
  ON events(project_id, received_at DESC, issue_id);

-- +goose Down
DROP INDEX IF EXISTS idx_events_project_received_issue;
