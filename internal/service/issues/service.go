package issues

import (
	"context"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type Repository interface {
	ListIssues(context.Context) ([]domain.Issue, error)
	ListIssuesFiltered(context.Context, domain.IssueFilter) ([]domain.Issue, error)
	GetIssue(context.Context, string) (domain.Issue, error)
	ResolveIssue(context.Context, string) (domain.Issue, error)
	ReopenIssue(context.Context, string) (domain.Issue, error)
	MuteIssue(ctx context.Context, id, muteMode string) (domain.Issue, error)
	UnmuteIssue(context.Context, string) (domain.Issue, error)
	HourlyEventCounts(context.Context, []int64) (map[int64][24]int, error)
	IssueRowIDByDisplayID(context.Context, string) (int64, error)
	ListIssueEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]domain.Event, bool, error)
	GetEvent(context.Context, string) (domain.Event, error)
	ListRecentEvents(ctx context.Context, limit int, since time.Time) ([]domain.Event, error)
	ListFacetKeys(ctx context.Context, projectID int64) ([]string, error)
	ListFacetValues(ctx context.Context, projectID int64, key string) ([]string, error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "issues")}
}

func (s *Service) List(ctx context.Context) ([]domain.Issue, error) {
	issues, err := s.repo.ListIssues(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "list issues", "error", err)
		return nil, err
	}
	return issues, nil
}

func (s *Service) ListFiltered(ctx context.Context, filter domain.IssueFilter) ([]domain.Issue, error) {
	issues, err := s.repo.ListIssuesFiltered(ctx, filter)
	if err != nil {
		s.logger.ErrorContext(ctx, "list issues filtered", "error", err)
		return nil, err
	}
	return issues, nil
}

func (s *Service) Get(ctx context.Context, id string) (domain.Issue, error) {
	issue, err := s.repo.GetIssue(ctx, id)
	if err != nil {
		s.logger.ErrorContext(ctx, "get issue", "issue_id", id, "error", err)
		return domain.Issue{}, err
	}
	return issue, nil
}

func (s *Service) Resolve(ctx context.Context, id string) (domain.Issue, error) {
	issue, err := s.repo.ResolveIssue(ctx, id)
	if err != nil {
		s.logger.ErrorContext(ctx, "resolve issue", "issue_id", id, "error", err)
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue resolved", "issue_id", id)
	return issue, nil
}

func (s *Service) Reopen(ctx context.Context, id string) (domain.Issue, error) {
	issue, err := s.repo.ReopenIssue(ctx, id)
	if err != nil {
		s.logger.ErrorContext(ctx, "reopen issue", "issue_id", id, "error", err)
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue reopened", "issue_id", id)
	return issue, nil
}

func (s *Service) Mute(ctx context.Context, id, muteMode string) (domain.Issue, error) {
	issue, err := s.repo.MuteIssue(ctx, id, muteMode)
	if err != nil {
		s.logger.ErrorContext(ctx, "mute issue", "issue_id", id, "mute_mode", muteMode, "error", err)
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue muted", "issue_id", id, "mute_mode", muteMode)
	return issue, nil
}

func (s *Service) Unmute(ctx context.Context, id string) (domain.Issue, error) {
	issue, err := s.repo.UnmuteIssue(ctx, id)
	if err != nil {
		s.logger.ErrorContext(ctx, "unmute issue", "issue_id", id, "error", err)
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue unmuted", "issue_id", id)
	return issue, nil
}

func (s *Service) HourlyEventCounts(ctx context.Context, issueIDs []int64) (map[int64][24]int, error) {
	counts, err := s.repo.HourlyEventCounts(ctx, issueIDs)
	if err != nil {
		s.logger.ErrorContext(ctx, "hourly event counts", "error", err)
		return nil, err
	}
	return counts, nil
}

func (s *Service) RowIDByDisplayID(ctx context.Context, displayID string) (int64, error) {
	return s.repo.IssueRowIDByDisplayID(ctx, displayID)
}

func (s *Service) ListEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]domain.Event, bool, error) {
	events, hasMore, err := s.repo.ListIssueEvents(ctx, issueID, limit, beforeID)
	if err != nil {
		s.logger.ErrorContext(ctx, "list events", "issue_id", issueID, "error", err)
		return nil, false, err
	}
	return events, hasMore, nil
}

func (s *Service) GetEvent(ctx context.Context, id string) (domain.Event, error) {
	evt, err := s.repo.GetEvent(ctx, id)
	if err != nil {
		s.logger.ErrorContext(ctx, "get event", "event_id", id, "error", err)
		return domain.Event{}, err
	}
	return evt, nil
}

func (s *Service) ListLiveEvents(ctx context.Context, limit int, since time.Time) ([]domain.Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-15 * time.Minute)
	}
	events, err := s.repo.ListRecentEvents(ctx, limit, since)
	if err != nil {
		s.logger.ErrorContext(ctx, "list live events", "error", err)
		return nil, err
	}
	return events, nil
}

func (s *Service) ListFacetKeys(ctx context.Context, projectID int64) ([]string, error) {
	return s.repo.ListFacetKeys(ctx, projectID)
}

func (s *Service) ListFacetValues(ctx context.Context, projectID int64, key string) ([]string, error) {
	return s.repo.ListFacetValues(ctx, projectID, key)
}
