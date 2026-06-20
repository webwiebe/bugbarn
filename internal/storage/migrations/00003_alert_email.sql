-- +goose Up
ALTER TABLE alerts ADD COLUMN email_to TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE alerts DROP COLUMN email_to;
