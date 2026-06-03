-- +goose Up
ALTER TABLE alerts ADD COLUMN param TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE alerts DROP COLUMN param;
