-- +goose Up
-- Check-in monitors: a scheduled job or worker reports "ok" or "error" with a
-- project key, and a monitor that stays silent past interval + grace opens an
-- issue. One row per monitor holds the configuration and the latest signal.
-- There is no per-check-in history, so nothing grows with the check-in rate.
CREATE TABLE IF NOT EXISTS monitors (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  interval_s INTEGER NOT NULL,
  grace_s INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  last_checkin_at TEXT NOT NULL DEFAULT '',
  last_status TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'ok',
  state_since TEXT NOT NULL DEFAULT '',
  last_emitted_at TEXT NOT NULL DEFAULT '',
  UNIQUE (project_id, slug)
);

-- +goose Down
DROP TABLE IF EXISTS monitors;
