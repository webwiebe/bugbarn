package issues

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
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
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.List")
	defer span.End()
	issues, err := s.repo.ListIssues(ctx)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "list issues", "error", err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("count", len(issues)))
	return issues, nil
}

func (s *Service) ListFiltered(ctx context.Context, filter domain.IssueFilter) ([]domain.Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.ListFiltered")
	defer span.End()
	issues, err := s.repo.ListIssuesFiltered(ctx, filter)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "list issues filtered", "error", err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("count", len(issues)))
	return issues, nil
}

func (s *Service) Get(ctx context.Context, id string) (domain.Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.Get",
		trace.WithAttributes(attribute.String("issue_id", id)))
	defer span.End()
	issue, err := s.repo.GetIssue(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "get issue", "issue_id", id, "error", err)
		return domain.Issue{}, err
	}
	return issue, nil
}

func (s *Service) Resolve(ctx context.Context, id string) (domain.Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.Resolve",
		trace.WithAttributes(attribute.String("issue_id", id)))
	defer span.End()
	issue, err := s.repo.ResolveIssue(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "resolve issue", "issue_id", id, "error", err)
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue resolved", "issue_id", id)
	return issue, nil
}

func (s *Service) Reopen(ctx context.Context, id string) (domain.Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.Reopen",
		trace.WithAttributes(attribute.String("issue_id", id)))
	defer span.End()
	issue, err := s.repo.ReopenIssue(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "reopen issue", "issue_id", id, "error", err)
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue reopened", "issue_id", id)
	return issue, nil
}

func (s *Service) Mute(ctx context.Context, id, muteMode string) (domain.Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.Mute",
		trace.WithAttributes(attribute.String("issue_id", id), attribute.String("mute_mode", muteMode)))
	defer span.End()
	issue, err := s.repo.MuteIssue(ctx, id, muteMode)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "mute issue", "issue_id", id, "mute_mode", muteMode, "error", err)
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue muted", "issue_id", id, "mute_mode", muteMode)
	return issue, nil
}

func (s *Service) Unmute(ctx context.Context, id string) (domain.Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.Unmute",
		trace.WithAttributes(attribute.String("issue_id", id)))
	defer span.End()
	issue, err := s.repo.UnmuteIssue(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "unmute issue", "issue_id", id, "error", err)
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue unmuted", "issue_id", id)
	return issue, nil
}

func (s *Service) HourlyEventCounts(ctx context.Context, issueIDs []int64) (map[int64][24]int, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.HourlyEventCounts")
	defer span.End()
	counts, err := s.repo.HourlyEventCounts(ctx, issueIDs)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "hourly event counts", "error", err)
		return nil, err
	}
	return counts, nil
}

func (s *Service) RowIDByDisplayID(ctx context.Context, displayID string) (int64, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.RowIDByDisplayID",
		trace.WithAttributes(attribute.String("display_id", displayID)))
	defer span.End()
	rowID, err := s.repo.IssueRowIDByDisplayID(ctx, displayID)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return rowID, err
}

func (s *Service) ListEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]domain.Event, bool, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.ListEvents",
		trace.WithAttributes(attribute.String("issue_id", issueID), attribute.Int("limit", limit)))
	defer span.End()
	events, hasMore, err := s.repo.ListIssueEvents(ctx, issueID, limit, beforeID)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "list events", "issue_id", issueID, "error", err)
		return nil, false, err
	}
	span.SetAttributes(attribute.Int("count", len(events)))
	return events, hasMore, nil
}

func (s *Service) GetEvent(ctx context.Context, id string) (domain.Event, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.GetEvent",
		trace.WithAttributes(attribute.String("event_id", id)))
	defer span.End()
	evt, err := s.repo.GetEvent(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "get event", "event_id", id, "error", err)
		return domain.Event{}, err
	}
	return evt, nil
}

func (s *Service) ListLiveEvents(ctx context.Context, limit int, since time.Time) ([]domain.Event, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.ListLiveEvents")
	defer span.End()
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-15 * time.Minute)
	}
	events, err := s.repo.ListRecentEvents(ctx, limit, since)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "list live events", "error", err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("count", len(events)))
	return events, nil
}

func (s *Service) ListFacetKeys(ctx context.Context, projectID int64) ([]string, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.ListFacetKeys")
	defer span.End()
	keys, err := s.repo.ListFacetKeys(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return keys, err
}

func (s *Service) ListFacetValues(ctx context.Context, projectID int64, key string) ([]string, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.ListFacetValues",
		trace.WithAttributes(attribute.String("facet_key", key)))
	defer span.End()
	vals, err := s.repo.ListFacetValues(ctx, projectID, key)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return vals, err
}
