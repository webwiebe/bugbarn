package issues

import (
	"context"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Repository defines the data access contract for issue operations.
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
	repo Repository
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]domain.Issue, error) {
	return s.repo.ListIssues(ctx)
}

func (s *Service) ListFiltered(ctx context.Context, filter domain.IssueFilter) ([]domain.Issue, error) {
	return s.repo.ListIssuesFiltered(ctx, filter)
}

func (s *Service) Get(ctx context.Context, id string) (domain.Issue, error) {
	return s.repo.GetIssue(ctx, id)
}

func (s *Service) Resolve(ctx context.Context, id string) (domain.Issue, error) {
	return s.repo.ResolveIssue(ctx, id)
}

func (s *Service) Reopen(ctx context.Context, id string) (domain.Issue, error) {
	return s.repo.ReopenIssue(ctx, id)
}

func (s *Service) Mute(ctx context.Context, id, muteMode string) (domain.Issue, error) {
	return s.repo.MuteIssue(ctx, id, muteMode)
}

func (s *Service) Unmute(ctx context.Context, id string) (domain.Issue, error) {
	return s.repo.UnmuteIssue(ctx, id)
}

func (s *Service) HourlyEventCounts(ctx context.Context, issueIDs []int64) (map[int64][24]int, error) {
	return s.repo.HourlyEventCounts(ctx, issueIDs)
}

func (s *Service) RowIDByDisplayID(ctx context.Context, displayID string) (int64, error) {
	return s.repo.IssueRowIDByDisplayID(ctx, displayID)
}

func (s *Service) ListEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]domain.Event, bool, error) {
	return s.repo.ListIssueEvents(ctx, issueID, limit, beforeID)
}

func (s *Service) GetEvent(ctx context.Context, id string) (domain.Event, error) {
	return s.repo.GetEvent(ctx, id)
}

func (s *Service) ListLiveEvents(ctx context.Context, limit int, since time.Time) ([]domain.Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-15 * time.Minute)
	}
	return s.repo.ListRecentEvents(ctx, limit, since)
}

func (s *Service) ListFacetKeys(ctx context.Context, projectID int64) ([]string, error) {
	return s.repo.ListFacetKeys(ctx, projectID)
}

func (s *Service) ListFacetValues(ctx context.Context, projectID int64, key string) ([]string, error) {
	return s.repo.ListFacetValues(ctx, projectID, key)
}
