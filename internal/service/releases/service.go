package releases

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

type Repository interface {
	ListReleases(context.Context) ([]domain.Release, error)
	GetRelease(context.Context, string) (domain.Release, error)
	CreateRelease(context.Context, domain.Release) (domain.Release, error)
	UpdateRelease(context.Context, string, domain.Release) (domain.Release, error)
	DeleteRelease(context.Context, string) error

	UploadSourceMap(context.Context, domain.SourceMapUpload) (domain.SourceMap, error)
	ListSourceMaps(context.Context) ([]domain.SourceMapMeta, error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "releases")}
}

func (s *Service) List(ctx context.Context) ([]domain.Release, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.releases.List")
	defer span.End()
	releases, err := s.repo.ListReleases(ctx)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return releases, err
}

func (s *Service) Get(ctx context.Context, id string) (domain.Release, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.releases.Get",
		trace.WithAttributes(attribute.String("release_id", id)))
	defer span.End()
	release, err := s.repo.GetRelease(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "get release", "release_id", id, "error", err)
		return domain.Release{}, err
	}
	return release, nil
}

func (s *Service) Create(ctx context.Context, release domain.Release) (domain.Release, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.releases.Create")
	defer span.End()
	created, err := s.repo.CreateRelease(ctx, release)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "create release", "name", release.Name, "error", err)
		return domain.Release{}, err
	}
	s.logger.InfoContext(ctx, "release created", "release_id", created.ID, "name", created.Name)
	return created, nil
}

func (s *Service) Update(ctx context.Context, id string, release domain.Release) (domain.Release, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.releases.Update",
		trace.WithAttributes(attribute.String("release_id", id)))
	defer span.End()
	updated, err := s.repo.UpdateRelease(ctx, id, release)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "update release", "release_id", id, "error", err)
		return domain.Release{}, err
	}
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.releases.Delete",
		trace.WithAttributes(attribute.String("release_id", id)))
	defer span.End()
	if err := s.repo.DeleteRelease(ctx, id); err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "delete release", "release_id", id, "error", err)
		return err
	}
	s.logger.InfoContext(ctx, "release deleted", "release_id", id)
	return nil
}

func (s *Service) UploadSourceMap(ctx context.Context, upload domain.SourceMapUpload) (domain.SourceMap, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.releases.UploadSourceMap")
	defer span.End()
	sm, err := s.repo.UploadSourceMap(ctx, upload)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "upload source map", "release", upload.Release, "error", err)
		return domain.SourceMap{}, err
	}
	s.logger.InfoContext(ctx, "source map uploaded", "release", upload.Release, "bundle_url", upload.BundleURL)
	return sm, nil
}

func (s *Service) ListSourceMaps(ctx context.Context) ([]domain.SourceMapMeta, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.releases.ListSourceMaps")
	defer span.End()
	maps, err := s.repo.ListSourceMaps(ctx)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return maps, err
}
