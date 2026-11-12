-- +goose Up
-- Whether the silence rules (bugbarn#252) judge this project. Empty means watched;
-- 'off' opts the project out, for a test project or an app shut down on purpose.
--
-- O(1): ADD COLUMN with a constant default records the default in the schema
-- and does not rewrite the projects table.
ALTER TABLE projects ADD COLUMN silence_watch TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE projects DROP COLUMN silence_watch;
