package analytics

import (
	"context"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

type Repository interface {
	InsertPageView(context.Context, analytics.PageView) error
	QueryOverview(context.Context, analytics.Query) (analytics.OverviewResult, error)
	QueryPages(context.Context, analytics.Query) ([]analytics.PageStat, error)
	QueryTimeline(context.Context, analytics.Query, string) ([]analytics.TimelineBucket, error)
	QueryReferrers(context.Context, analytics.Query) ([]analytics.ReferrerStat, error)
	QuerySegments(context.Context, analytics.Query, string) ([]analytics.SegmentBucket, error)
	QueryPageFlow(context.Context, analytics.Query, string) (analytics.PageFlowResult, error)
	QueryScrollDepth(context.Context, analytics.Query, string) (analytics.ScrollDepthResult, error)
	QueryDropout(context.Context, analytics.Query) ([]analytics.DropoutStat, error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "analytics")}
}

func (s *Service) InsertPageView(ctx context.Context, pv analytics.PageView) error {
	return s.repo.InsertPageView(ctx, pv)
}

func (s *Service) QueryOverview(ctx context.Context, q analytics.Query) (analytics.OverviewResult, error) {
	result, err := s.repo.QueryOverview(ctx, q)
	if err != nil {
		s.logger.ErrorContext(ctx, "query overview", "error", err)
		return analytics.OverviewResult{}, err
	}
	return result, nil
}

func (s *Service) QueryPages(ctx context.Context, q analytics.Query) ([]analytics.PageStat, error) {
	return s.repo.QueryPages(ctx, q)
}

func (s *Service) QueryTimeline(ctx context.Context, q analytics.Query, granularity string) ([]analytics.TimelineBucket, error) {
	return s.repo.QueryTimeline(ctx, q, granularity)
}

func (s *Service) QueryReferrers(ctx context.Context, q analytics.Query) ([]analytics.ReferrerStat, error) {
	return s.repo.QueryReferrers(ctx, q)
}

func (s *Service) QuerySegments(ctx context.Context, q analytics.Query, dimKey string) ([]analytics.SegmentBucket, error) {
	return s.repo.QuerySegments(ctx, q, dimKey)
}

func (s *Service) QueryPageFlow(ctx context.Context, q analytics.Query, pathname string) (analytics.PageFlowResult, error) {
	return s.repo.QueryPageFlow(ctx, q, pathname)
}

func (s *Service) QueryScrollDepth(ctx context.Context, q analytics.Query, pathname string) (analytics.ScrollDepthResult, error) {
	return s.repo.QueryScrollDepth(ctx, q, pathname)
}

func (s *Service) QueryDropout(ctx context.Context, q analytics.Query) ([]analytics.DropoutStat, error) {
	return s.repo.QueryDropout(ctx, q)
}
