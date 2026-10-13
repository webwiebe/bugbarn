-- +goose Up
-- Cron monitors: a 5-field cron expression plus an IANA time zone replace the
-- interval for jobs that run at fixed times. Both stay empty for interval
-- monitors, which keep using interval_s.
ALTER TABLE monitors ADD COLUMN schedule TEXT NOT NULL DEFAULT '';
ALTER TABLE monitors ADD COLUMN timezone TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE monitors DROP COLUMN timezone;
ALTER TABLE monitors DROP COLUMN schedule;
