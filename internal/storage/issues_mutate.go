package storage

import (
	"context"
	"fmt"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// MuteIssue sets an issue to muted status with the given mute mode.
// muteMode must be one of "until_regression" or "forever".
func (s *IssueStore) MuteIssue(ctx context.Context, issueID string, muteMode string) (Issue, error) {
	if muteMode != "until_regression" && muteMode != "forever" {
		return Issue{}, apperr.InvalidInput(fmt.Sprintf("invalid mute_mode %q: must be 'until_regression' or 'forever'", muteMode), nil)
	}
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
	}
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}
	if projectID != 0 {
		_, err = s.db.ExecContext(ctx, `
UPDATE issues SET status = 'muted', mute_mode = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND project_id = ?`,
			muteMode, rowID, projectID)
	} else {
		_, err = s.db.ExecContext(ctx, `
UPDATE issues SET status = 'muted', mute_mode = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			muteMode, rowID)
	}
	if err != nil {
		return Issue{}, err
	}
	return s.GetIssue(ctx, issueID)
}

// UnmuteIssue clears mute status and sets the issue back to unresolved.
func (s *IssueStore) UnmuteIssue(ctx context.Context, issueID string) (Issue, error) {
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
	}
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}
	var err2 error
	if projectID != 0 {
		_, err2 = s.db.ExecContext(ctx, `
UPDATE issues SET status = 'unresolved', mute_mode = '', updated_at = CURRENT_TIMESTAMP WHERE id = ? AND project_id = ?`,
			rowID, projectID)
	} else {
		_, err2 = s.db.ExecContext(ctx, `
UPDATE issues SET status = 'unresolved', mute_mode = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			rowID)
	}
	if err2 != nil {
		return Issue{}, err2
	}
	return s.GetIssue(ctx, issueID)
}
