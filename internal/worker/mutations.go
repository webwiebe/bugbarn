package worker

import (
	"context"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
)

// IssueMutator is what the worker needs from the store to apply a queued admin
// mutation. *storage.Store satisfies it.
type IssueMutator interface {
	ResolveIssue(ctx context.Context, issueID string) (domain.Issue, error)
	ReopenIssue(ctx context.Context, issueID string) (domain.Issue, error)
	MuteIssue(ctx context.Context, issueID, muteMode string) (domain.Issue, error)
	UnmuteIssue(ctx context.Context, issueID string) (domain.Issue, error)
}

// applyMutation executes a single queued admin mutation against the store.
func applyMutation(ctx context.Context, store IssueMutator, r mutqueue.Record) error {
	switch r.Op {
	case mutqueue.OpResolve:
		_, err := store.ResolveIssue(ctx, r.IssueID)
		return err
	case mutqueue.OpReopen:
		_, err := store.ReopenIssue(ctx, r.IssueID)
		return err
	case mutqueue.OpMute:
		_, err := store.MuteIssue(ctx, r.IssueID, r.MuteMode)
		return err
	case mutqueue.OpUnmute:
		_, err := store.UnmuteIssue(ctx, r.IssueID)
		return err
	default:
		slog.Warn("mutqueue: unknown op, skipping", "op", r.Op, "issue_id", r.IssueID)
		return nil
	}
}
