package releases

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Repository defines the data access contract for releases and source maps.
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
	repo Repository
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]domain.Release, error) {
	return s.repo.ListReleases(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (domain.Release, error) {
	return s.repo.GetRelease(ctx, id)
}

func (s *Service) Create(ctx context.Context, release domain.Release) (domain.Release, error) {
	return s.repo.CreateRelease(ctx, release)
}

func (s *Service) Update(ctx context.Context, id string, release domain.Release) (domain.Release, error) {
	return s.repo.UpdateRelease(ctx, id, release)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.DeleteRelease(ctx, id)
}

func (s *Service) UploadSourceMap(ctx context.Context, upload domain.SourceMapUpload) (domain.SourceMap, error) {
	return s.repo.UploadSourceMap(ctx, upload)
}

func (s *Service) ListSourceMaps(ctx context.Context) ([]domain.SourceMapMeta, error) {
	return s.repo.ListSourceMaps(ctx)
}
