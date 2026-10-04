-- +goose Up
-- Small key/value state for the writer's background jobs. The hourly rollup
-- keeps its cursor here (the first hour it has not rolled up yet), written in
-- the same transaction as that hour's rows so a restart neither skips nor
-- repeats an hour.
CREATE TABLE meta (
    key   TEXT    PRIMARY KEY,
    value INTEGER NOT NULL
) WITHOUT ROWID;

-- Retention deletes hosts not seen for the hourly window.
CREATE INDEX idx_hosts_last_seen ON hosts (last_seen);

-- +goose Down
DROP INDEX idx_hosts_last_seen;
DROP TABLE meta;
