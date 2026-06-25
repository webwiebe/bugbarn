package storage

import (
	"context"
	"time"
)

func (s *IssueStore) ResolveIssue(ctx context.Context, issueID string) (Issue, error) {
	return s.setIssueStatus(ctx, issueID, "resolved")
}

func (s *IssueStore) ReopenIssue(ctx context.Context, issueID string) (Issue, error) {
	return s.setIssueStatus(ctx, issueID, "unresolved")
}

func (s *IssueStore) setIssueStatus(ctx context.Context, issueID, status string) (Issue, error) {
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Issue{}, err
	}
	defer tx.Rollback()

	now := formatTime(time.Now().UTC())
	_, execErr := tx.ExecContext(ctx, `
UPDATE issues
SET status = ?,
	resolved_at = CASE WHEN ? = 'resolved' THEN ? ELSE resolved_at END,
	reopened_at = CASE WHEN ? = 'unresolved' THEN ? ELSE reopened_at END,
	updated_at = CURRENT_TIMESTAMP
WHERE id = ?`, status, status, now, status, now, rowID)
	if execErr != nil {
		return Issue{}, execErr
	}
	if err := tx.Commit(); err != nil {
		return Issue{}, err
	}

	return s.GetIssue(ctx, issueID)
}
