-- +goose Up
-- The error timeline on the Issues page sums sample_weight per project and hour
-- over every error event of the last 30 days:
--
--   SELECT project_id, substr(received_at, 1, 13), SUM(sample_weight)
--   FROM events WHERE received_at >= ? AND severity IN (...) GROUP BY 1, 2
--
-- Every existing index on events either leads with project_id or holds only
-- (received_at, id), so the query would visit the events table row by row, and
-- those rows carry event_json. This index holds the four columns the query
-- reads, with received_at first for the range, so the planner answers it from
-- the index alone.
--
-- Cost: one more index for the ingest path to maintain per inserted event.
CREATE INDEX IF NOT EXISTS idx_events_error_timeline
    ON events(received_at, severity, project_id, sample_weight);

-- +goose Down
DROP INDEX IF EXISTS idx_events_error_timeline;
