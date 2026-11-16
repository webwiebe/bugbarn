-- +goose Up
-- One project per product (bugbarn#282): an environment is an attribute of the
-- data, not a project. A merged source project stays as an archived row that
-- redirects to its target. Its ingest keys and slug keep working and resolve to
-- the target project plus the environment the source stood for.
--
-- O(1): ADD COLUMN with a constant default does not rewrite the projects table.
ALTER TABLE projects ADD COLUMN merged_into INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN merged_environment TEXT NOT NULL DEFAULT '';

-- One row per merge: the record of what moved, with the counts. The source row is
-- kept (status archived), so nothing is deleted outright.
CREATE TABLE IF NOT EXISTS project_merges (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id           INTEGER NOT NULL,
    source_slug         TEXT NOT NULL,
    target_id           INTEGER NOT NULL,
    target_slug         TEXT NOT NULL,
    environment         TEXT NOT NULL,
    issue_number_offset INTEGER NOT NULL DEFAULT 0,
    counts_json         TEXT NOT NULL DEFAULT '{}',
    merged_at           TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS project_merges;
ALTER TABLE projects DROP COLUMN merged_environment;
ALTER TABLE projects DROP COLUMN merged_into;
