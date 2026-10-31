-- +goose Up
-- A setup key is derived from the session secret and the project slug, so removing
-- its api_keys row does not stop it: the setup fallback accepts it again. A row here
-- is what ends one. It is keyed by slug and needs no project or api_keys row, because
-- a slug can be in use without either.
CREATE TABLE setup_key_revocations (
    slug TEXT PRIMARY KEY,
    revoked_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE setup_key_revocations;
