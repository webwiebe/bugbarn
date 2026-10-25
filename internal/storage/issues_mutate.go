package storage

import (
	"context"
	"fmt"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// MuteIssue sets an issue to muted status with the given mute mode.
// muteMode must be one of "until_regression" or "forever".
//
// The row is addressed by its resolved row ID alone, like ResolveIssue: the
// request's project scope already shaped the display-ID lookup, and a second
// project filter on the UPDATE matched zero rows whenever the context held the
// default project (an API key without X-BugBarn-Project), leaving the issue
// unmuted while the call reported success.
func (s *IssueStore) MuteIssue(ctx context.Context, issueID string, muteMode string) (Issue, error) {
	if muteMode != "until_regression" && muteMode != "forever" {
		return Issue{}, apperr.InvalidInput(fmt.Sprintf("invalid mute_mode %q: must be 'until_regression' or 'forever'", muteMode), nil)
	}
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE issues SET status = 'muted', mute_mode = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		muteMode, rowID); err != nil {
		return Issue{}, wrapErr(err, "mute issue")
	}
	return s.GetIssue(ctx, issueID)
}

// UnmuteIssue clears mute status and sets the issue back to unresolved.
func (s *IssueStore) UnmuteIssue(ctx context.Context, issueID string) (Issue, error) {
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE issues SET status = 'unresolved', mute_mode = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		rowID); err != nil {
		return Issue{}, wrapErr(err, "unmute issue")
	}
	return s.GetIssue(ctx, issueID)
}
