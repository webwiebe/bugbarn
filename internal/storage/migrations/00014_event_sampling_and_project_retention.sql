-- +goose Up
-- Bound how much one noisy fingerprint can cost us, and let a project carry its
-- own retention window.
--
-- Production reached 1,842,279 events for a single fingerprint (spanbarn
-- "logs insert error", August 2026) and nothing stopped it. `events` is 94.7% of
-- the database, so one client loop in one project decides the size of the whole
-- file and crowds out everyone else.
--
-- `sample_weight` is how many real events a stored row stands for. Once an issue
-- is past the sampling threshold the write path keeps one row in K and records
-- K here, so every derived count — the digest, the 24h sparkline, per-project
-- usage — sums weights instead of counting rows and keeps reporting real
-- volume. It defaults to 1, which is exactly what every existing row means.
--
-- `retention_days` is NULL for "inherit the global window" and may only be
-- shorter than it: the global sweep would delete anything older anyway, so a
-- longer per-project value would be a promise we cannot keep.
--
-- `sampling_mode` is '' (inherit the global default), 'on' or 'off'.
--
-- This migration is O(1). SQLite's ALTER TABLE ADD COLUMN with a constant
-- default records the default in the schema and never rewrites the table, so
-- adding a column to the 831k-row events table costs the same as adding one to
-- an empty one. Worth stating plainly: two earlier migrations here did rewrite
-- large objects (00011 took 7m17s on production) and this one must not be read
-- as another of those.
ALTER TABLE events ADD COLUMN sample_weight INTEGER NOT NULL DEFAULT 1;
ALTER TABLE projects ADD COLUMN retention_days INTEGER;
ALTER TABLE projects ADD COLUMN sampling_mode TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE projects DROP COLUMN sampling_mode;
ALTER TABLE projects DROP COLUMN retention_days;
ALTER TABLE events DROP COLUMN sample_weight;
