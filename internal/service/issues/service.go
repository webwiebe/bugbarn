package issues

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// isClientError returns true for errors that represent bad caller input rather
// than unexpected server-side failures. These are not logged at ERROR level to
// avoid self-reporting noise from normal 400/404 responses.
func isClientError(err error) bool {
	return errors.Is(err, apperr.ErrInvalidInput) ||
		errors.Is(err, apperr.ErrNotFound) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

type Repository interface {
	ListIssues(context.Context) ([]domain.Issue, error)
	ListIssuesFiltered(context.Context, domain.IssueFilter) ([]domain.Issue, error)
	GetIssue(context.Context, string) (domain.Issue, error)
	ResolveIssue(context.Context, string) (domain.Issue, error)
	ReopenIssue(context.Context, string) (domain.Issue, error)
	MuteIssue(ctx context.Context, id, muteMode string) (domain.Issue, error)
	UnmuteIssue(context.Context, string) (domain.Issue, error)
	HourlyEventCounts(context.Context, []int64) (map[int64][24]int, error)
	IssueStatusCounts(context.Context) (domain.IssueCounts, error)
	IssueRowIDByDisplayID(context.Context, string) (int64, error)
	ListIssueEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]domain.Event, bool, error)
	IssueReleases(ctx context.Context, issueID string, sample int) ([]domain.IssueRelease, int, error)
	GetEvent(context.Context, string) (domain.Event, error)
	ListRecentEvents(ctx context.Context, limit int, since time.Time) ([]domain.Event, error)
	ListFacetKeys(ctx context.Context, projectID int64) ([]string, error)
	ListFacetValues(ctx context.Context, projectID int64, key string) ([]string, error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
	counts countsCache
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
		if !apperr.IsContextError(err) {
			s.logger.ErrorContext(ctx, "list issues", "error", err)
		}
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
		if !apperr.IsContextError(err) {
			s.logger.ErrorContext(ctx, "list issues filtered", "error", err)
		}
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
		if !isClientError(err) {
			s.logger.ErrorContext(ctx, "get issue", "issue_id", id, "error", err)
		}
		return domain.Issue{}, err
	}
	return issue, nil
}

// statusChange names one status transition for tracing and logging.
type statusChange struct {
	span, failed, done string
	apply              func(context.Context, string) (domain.Issue, error)
}

// changeStatus runs one transition, logs it, and drops the cached status counts
// so the next issue list reflects the change.
func (s *Service) changeStatus(ctx context.Context, id string, c statusChange) (domain.Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, c.span,
		trace.WithAttributes(attribute.String("issue_id", id)))
	defer span.End()
	issue, err := c.apply(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		if !isClientError(err) {
			s.logger.ErrorContext(ctx, c.failed, "issue_id", id, "error", err)
		}
		return domain.Issue{}, err
	}
	s.invalidateCounts()
	s.logger.InfoContext(ctx, c.done, "issue_id", id)
	return issue, nil
}

func (s *Service) Resolve(ctx context.Context, id string) (domain.Issue, error) {
	return s.changeStatus(ctx, id, statusChange{"service.issues.Resolve", "resolve issue", "issue resolved", s.repo.ResolveIssue})
}

func (s *Service) Reopen(ctx context.Context, id string) (domain.Issue, error) {
	return s.changeStatus(ctx, id, statusChange{"service.issues.Reopen", "reopen issue", "issue reopened", s.repo.ReopenIssue})
}

func (s *Service) Mute(ctx context.Context, id, muteMode string) (domain.Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.Mute",
		trace.WithAttributes(attribute.String("issue_id", id), attribute.String("mute_mode", muteMode)))
	defer span.End()
	issue, err := s.repo.MuteIssue(ctx, id, muteMode)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		if !isClientError(err) {
			s.logger.ErrorContext(ctx, "mute issue", "issue_id", id, "mute_mode", muteMode, "error", err)
		}
		return domain.Issue{}, err
	}
	s.logger.InfoContext(ctx, "issue muted", "issue_id", id, "mute_mode", muteMode)
	s.invalidateCounts()
	return issue, nil
}

func (s *Service) Unmute(ctx context.Context, id string) (domain.Issue, error) {
	return s.changeStatus(ctx, id, statusChange{"service.issues.Unmute", "unmute issue", "issue unmuted", s.repo.UnmuteIssue})
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
		if !isClientError(err) {
			s.logger.ErrorContext(ctx, "list events", "issue_id", issueID, "error", err)
		}
		return nil, false, err
	}
	span.SetAttributes(attribute.Int("count", len(events)))
	return events, hasMore, nil
}

// ReleaseSampleSize is how many of an issue's latest events the release counts
// are taken from.
const ReleaseSampleSize = 500

// IssueReleases counts the releases among the issue's latest events. It returns
// the releases (most recently seen first) and how many events were looked at.
func (s *Service) IssueReleases(ctx context.Context, issueID string) ([]domain.IssueRelease, int, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.IssueReleases",
		trace.WithAttributes(attribute.String("issue_id", issueID)))
	defer span.End()
	releases, sampled, err := s.repo.IssueReleases(ctx, issueID, ReleaseSampleSize)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		if !isClientError(err) {
			s.logger.ErrorContext(ctx, "issue releases", "issue_id", issueID, "error", err)
		}
		return nil, 0, err
	}
	return releases, sampled, nil
}

func (s *Service) GetEvent(ctx context.Context, id string) (domain.Event, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.issues.GetEvent",
		trace.WithAttributes(attribute.String("event_id", id)))
	defer span.End()
	evt, err := s.repo.GetEvent(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		if !isClientError(err) && !apperr.IsContextError(err) {
			s.logger.ErrorContext(ctx, "get event", "event_id", id, "error", err)
		}
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
		span.SetStatus(codes.Error, err.Error())
		if !apperr.IsContextError(err) {
			s.logger.ErrorContext(ctx, "list live events", "error", err)
		}
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
