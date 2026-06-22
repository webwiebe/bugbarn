-- +goose Up
-- Events and logs ingested for a project that is still pending admin approval are
-- parked here instead of being persisted as issues. On approval the project's
-- backlog is drained through the normal persist pipeline and these rows deleted.
-- body_base64 holds the raw ingest payload so replay is byte-for-byte identical
-- to live ingest.
CREATE TABLE IF NOT EXISTS held_events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id   INTEGER NOT NULL,
  slug         TEXT    NOT NULL,
  kind         TEXT    NOT NULL,
  ingest_id    TEXT    NOT NULL DEFAULT '',
  received_at  TEXT    NOT NULL,
  content_type TEXT    NOT NULL DEFAULT '',
  body_base64  TEXT    NOT NULL,
  created_at   TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_held_events_project ON held_events(project_id, id);

-- +goose Down
DROP INDEX IF EXISTS idx_held_events_project;
DROP TABLE IF EXISTS held_events;
