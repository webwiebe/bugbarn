-- +goose Up
-- The release carried by the first event of an issue, set once when the issue is
-- created. Existing issues keep ''. ADD COLUMN with a constant default does not
-- rewrite the table.
ALTER TABLE issues ADD COLUMN first_release TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE issues DROP COLUMN first_release;
