package analytics

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/codes"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
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
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.InsertPageView")
	defer span.End()
	if err := s.repo.InsertPageView(ctx, pv); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func (s *Service) QueryOverview(ctx context.Context, q analytics.Query) (analytics.OverviewResult, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.QueryOverview")
	defer span.End()
	result, err := s.repo.QueryOverview(ctx, q)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "query overview", "error", err)
		return analytics.OverviewResult{}, err
	}
	return result, nil
}

func (s *Service) QueryPages(ctx context.Context, q analytics.Query) ([]analytics.PageStat, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.QueryPages")
	defer span.End()
	pages, err := s.repo.QueryPages(ctx, q)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return pages, err
}

func (s *Service) QueryTimeline(ctx context.Context, q analytics.Query, granularity string) ([]analytics.TimelineBucket, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.QueryTimeline")
	defer span.End()
	buckets, err := s.repo.QueryTimeline(ctx, q, granularity)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return buckets, err
}

func (s *Service) QueryReferrers(ctx context.Context, q analytics.Query) ([]analytics.ReferrerStat, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.QueryReferrers")
	defer span.End()
	refs, err := s.repo.QueryReferrers(ctx, q)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return refs, err
}

func (s *Service) QuerySegments(ctx context.Context, q analytics.Query, dimKey string) ([]analytics.SegmentBucket, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.QuerySegments")
	defer span.End()
	segments, err := s.repo.QuerySegments(ctx, q, dimKey)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return segments, err
}

func (s *Service) QueryPageFlow(ctx context.Context, q analytics.Query, pathname string) (analytics.PageFlowResult, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.QueryPageFlow")
	defer span.End()
	result, err := s.repo.QueryPageFlow(ctx, q, pathname)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return result, err
}

func (s *Service) QueryScrollDepth(ctx context.Context, q analytics.Query, pathname string) (analytics.ScrollDepthResult, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.QueryScrollDepth")
	defer span.End()
	result, err := s.repo.QueryScrollDepth(ctx, q, pathname)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return result, err
}

func (s *Service) QueryDropout(ctx context.Context, q analytics.Query) ([]analytics.DropoutStat, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.analytics.QueryDropout")
	defer span.End()
	stats, err := s.repo.QueryDropout(ctx, q)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return stats, err
}
