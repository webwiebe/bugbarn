-- +goose Up
-- The FunnelBarn page a project's "By surface" rows link to. Empty means no link.
-- A plain link: BugBarn never calls FunnelBarn and stores no key for it.
--
-- O(1): ADD COLUMN with a constant default records the default in the schema
-- and does not rewrite the projects table.
ALTER TABLE projects ADD COLUMN funnelbarn_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE projects DROP COLUMN funnelbarn_url;
