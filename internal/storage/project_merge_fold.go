package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// foldCollidingIssues folds each source issue whose fingerprint the target
// already holds into that target issue, then deletes the source issue.
// issues has UNIQUE(project_id, fingerprint), so moving such an issue would
// fail the whole merge. Events, regression rows and facets follow the issue;
// the target keeps its status, mute mode and representative event.
func foldCollidingIssues(ctx context.Context, tx *sql.Tx, sourceID, targetID int64) error {
	// Each fold deletes its source issue, so the next lookup finds the next pair.
	for {
		var from, into int64
		err := tx.QueryRowContext(ctx, `
			SELECT s.id, t.id FROM issues s
			JOIN issues t ON t.project_id = ? AND t.fingerprint = s.fingerprint
			WHERE s.project_id = ? LIMIT 1`, targetID, sourceID).Scan(&from, &into)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return apperr.Internal("merge: find colliding issues", err)
		}
		if err := foldIssue(ctx, tx, from, into); err != nil {
			return err
		}
	}
}

func foldIssue(ctx context.Context, tx *sql.Tx, from, into int64) error {
	steps := []struct {
		what string
		sql  string
		args []any
	}{
		{"events", `UPDATE events SET issue_id = ? WHERE issue_id = ?`, []any{into, from}},
		{"regression_events", `UPDATE regression_events SET issue_id = ? WHERE issue_id = ?`, []any{into, from}},
		{"facets", `INSERT OR IGNORE INTO issue_facets (project_id, issue_id, facet_key, facet_value)
			SELECT (SELECT project_id FROM issues WHERE id = ?), ?, facet_key, facet_value
			FROM issue_facets WHERE issue_id = ?`, []any{into, into, from}},
		{"source facets", `DELETE FROM issue_facets WHERE issue_id = ?`, []any{from}},
		{"counts", `UPDATE issues SET
			event_count = event_count + (SELECT event_count FROM issues WHERE id = ?),
			regression_count = regression_count + (SELECT regression_count FROM issues WHERE id = ?),
			first_seen = MIN(first_seen, (SELECT first_seen FROM issues WHERE id = ?)),
			last_seen = MAX(last_seen, (SELECT last_seen FROM issues WHERE id = ?)),
			updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, []any{from, from, from, from, into}},
		{"source issue", `DELETE FROM issues WHERE id = ?`, []any{from}},
	}
	for _, s := range steps {
		if _, err := tx.ExecContext(ctx, s.sql, s.args...); err != nil {
			return apperr.Internal("merge: fold "+s.what, err)
		}
	}
	return nil
}
