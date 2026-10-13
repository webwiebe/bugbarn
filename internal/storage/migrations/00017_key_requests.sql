-- +goose Up
-- Key requests: an agent asks for an ingest key for an active project and an
-- admin approves or denies it in the dashboard. The row never holds a key.
-- The key is minted when the agent claims the approved request, so the
-- plaintext only exists in that one response.
CREATE TABLE IF NOT EXISTS key_requests (
  id TEXT PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  poll_sha256 TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  resolved_at TEXT NOT NULL DEFAULT '',
  claimed_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_key_requests_project ON key_requests(project_id, status);

-- +goose Down
DROP TABLE IF EXISTS key_requests;
