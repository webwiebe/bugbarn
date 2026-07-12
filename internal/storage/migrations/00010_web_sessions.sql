-- +goose Up
-- Server-side web sessions (token-bound BFF pattern). The browser cookie is an
-- opaque random handle; id_hash is its SHA-256 hex digest so a database leak
-- never exposes usable session handles. OIDC logins store the iambarn tokens
-- here (never in the browser); local admin logins get a row with empty token
-- columns so one middleware serves both auth methods.
CREATE TABLE IF NOT EXISTS web_sessions (
  id_hash               TEXT PRIMARY KEY,
  username              TEXT NOT NULL,
  auth_method           TEXT NOT NULL, -- 'oidc' | 'local'
  idp_sub               TEXT NOT NULL DEFAULT '',
  idp_sid               TEXT NOT NULL DEFAULT '',
  id_token              TEXT NOT NULL DEFAULT '',
  access_token          TEXT NOT NULL DEFAULT '',
  refresh_token         TEXT NOT NULL DEFAULT '',
  access_expires_at     TEXT NOT NULL DEFAULT '',
  claims_json           TEXT NOT NULL DEFAULT '',
  created_at            TEXT NOT NULL,
  absolute_expires_at   TEXT NOT NULL,
  last_refresh_at       TEXT NOT NULL DEFAULT '',
  refresh_failing_since TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_web_sessions_sid ON web_sessions(idp_sid);
CREATE INDEX IF NOT EXISTS idx_web_sessions_sub ON web_sessions(idp_sub);
CREATE INDEX IF NOT EXISTS idx_web_sessions_abs_exp ON web_sessions(absolute_expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_web_sessions_abs_exp;
DROP INDEX IF EXISTS idx_web_sessions_sub;
DROP INDEX IF EXISTS idx_web_sessions_sid;
DROP TABLE IF EXISTS web_sessions;
