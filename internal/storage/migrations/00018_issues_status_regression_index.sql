-- +goose Up
-- Keep the per-status issue counts index-only.
--
-- The issues page shows how many issues each status holds, including how many
-- of the open ones came back after a regression:
--
--   SELECT status, regression_count > 0, COUNT(*) FROM issues
--   WHERE project_id = ? GROUP BY status, regression_count > 0
--
-- idx_issues_project_status_last_seen covers project_id and status but not
-- regression_count, so the query would read every issue row. issues is a small
-- table (thousands of rows), so the build takes milliseconds.
CREATE INDEX IF NOT EXISTS idx_issues_project_status_regression ON issues(project_id, status, regression_count);

-- +goose Down
DROP INDEX IF EXISTS idx_issues_project_status_regression;
