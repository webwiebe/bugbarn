package storage

import (
	"context"
	"database/sql"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// stampEnvironment gives the rows of a project that name no environment the
// environment env. It runs inside the merge transaction, before the rows move.
//
//   - issues: one issue_facets row under attributes.environment, the facet the
//     dashboard filters on, for every issue that has neither environment facet.
//   - releases: the environment column where it is empty.
//   - log_entries: the environment attribute in data_json where it is absent.
//
// Events keep their stored payload: the environment of an issue is its facet,
// and rewriting the payload of every event would cost a full table update.
func stampEnvironment(ctx context.Context, tx *sql.Tx, projectID int64, env string) error {
	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO issue_facets (project_id, issue_id, facet_key, facet_value)
SELECT i.project_id, i.id, ?, ?
FROM issues i
WHERE i.project_id = ?
  AND i.id NOT IN (
	SELECT issue_id FROM issue_facets WHERE project_id = ? AND facet_key IN (?, ?))`,
		AttributeEnvironmentFacetKey, env, projectID, projectID, EnvironmentFacetKey, AttributeEnvironmentFacetKey); err != nil {
		return apperr.Internal("merge: stamp issue environment", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE releases SET environment = ? WHERE project_id = ? AND (environment IS NULL OR environment = '')`,
		env, projectID); err != nil {
		return apperr.Internal("merge: stamp release environment", err)
	}
	path := logPath("environment")
	if _, err := tx.ExecContext(ctx, `
UPDATE log_entries SET data_json = json_set(data_json, ?, ?)
WHERE project_id = ? AND json_valid(data_json) AND json_extract(data_json, ?) IS NULL`,
		path, env, projectID, path); err != nil {
		return apperr.Internal("merge: stamp log environment", err)
	}
	return nil
}
