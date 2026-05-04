package projects

import (
	"context"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type Repository interface {
	ListProjects(context.Context) ([]domain.Project, error)
	CreateProject(ctx context.Context, name, slug string) (domain.Project, error)
	EnsureProject(ctx context.Context, slug string) (domain.Project, error)
	EnsureProjectPending(ctx context.Context, slug string) (domain.Project, error)
	ApproveProject(ctx context.Context, slug string) error
	ProjectBySlug(ctx context.Context, slug string) (domain.Project, error)
	DefaultProjectID() int64

	ListAPIKeys(context.Context) ([]domain.APIKey, error)
	CreateAPIKey(ctx context.Context, name string, projectID int64, keySHA256, scope string) (domain.APIKey, error)
	DeleteAPIKey(ctx context.Context, id int64) error
	EnsureSetupAPIKey(ctx context.Context, name string, projectID int64, keySHA256 string) error
	ValidAPIKeySHA256(ctx context.Context, keySHA256 string) (projectID int64, scope string, found bool, err error)
	TouchAPIKey(ctx context.Context, keySHA256 string) error

	GetSettings(context.Context) (map[string]string, error)
	UpdateSettings(context.Context, map[string]string) error
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "projects")}
}

func (s *Service) List(ctx context.Context) ([]domain.Project, error) {
	return s.repo.ListProjects(ctx)
}

func (s *Service) Create(ctx context.Context, name, slug string) (domain.Project, error) {
	proj, err := s.repo.CreateProject(ctx, name, slug)
	if err != nil {
		s.logger.ErrorContext(ctx, "create project", "slug", slug, "error", err)
		return domain.Project{}, err
	}
	s.logger.InfoContext(ctx, "project created", "slug", slug, "id", proj.ID)
	return proj, nil
}

func (s *Service) Ensure(ctx context.Context, slug string) (domain.Project, error) {
	return s.repo.EnsureProject(ctx, slug)
}

func (s *Service) EnsurePending(ctx context.Context, slug string) (domain.Project, error) {
	return s.repo.EnsureProjectPending(ctx, slug)
}

func (s *Service) Approve(ctx context.Context, slug string) error {
	if err := s.repo.ApproveProject(ctx, slug); err != nil {
		s.logger.ErrorContext(ctx, "approve project", "slug", slug, "error", err)
		return err
	}
	s.logger.InfoContext(ctx, "project approved", "slug", slug)
	return nil
}

func (s *Service) BySlug(ctx context.Context, slug string) (domain.Project, error) {
	proj, err := s.repo.ProjectBySlug(ctx, slug)
	if err != nil {
		s.logger.ErrorContext(ctx, "project by slug", "slug", slug, "error", err)
		return domain.Project{}, err
	}
	return proj, nil
}

func (s *Service) DefaultProjectID() int64 {
	return s.repo.DefaultProjectID()
}

func (s *Service) ListAPIKeys(ctx context.Context) ([]domain.APIKey, error) {
	return s.repo.ListAPIKeys(ctx)
}

func (s *Service) CreateAPIKey(ctx context.Context, name string, projectID int64, keySHA256, scope string) (domain.APIKey, error) {
	key, err := s.repo.CreateAPIKey(ctx, name, projectID, keySHA256, scope)
	if err != nil {
		s.logger.ErrorContext(ctx, "create api key", "name", name, "error", err)
		return domain.APIKey{}, err
	}
	s.logger.InfoContext(ctx, "api key created", "name", name, "project_id", projectID)
	return key, nil
}

func (s *Service) DeleteAPIKey(ctx context.Context, id int64) error {
	if err := s.repo.DeleteAPIKey(ctx, id); err != nil {
		s.logger.ErrorContext(ctx, "delete api key", "id", id, "error", err)
		return err
	}
	return nil
}

func (s *Service) EnsureSetupAPIKey(ctx context.Context, name string, projectID int64, keySHA256 string) error {
	return s.repo.EnsureSetupAPIKey(ctx, name, projectID, keySHA256)
}

func (s *Service) ValidAPIKeySHA256(ctx context.Context, keySHA256 string) (projectID int64, scope string, found bool, err error) {
	return s.repo.ValidAPIKeySHA256(ctx, keySHA256)
}

func (s *Service) TouchAPIKey(ctx context.Context, keySHA256 string) error {
	return s.repo.TouchAPIKey(ctx, keySHA256)
}

func (s *Service) GetSettings(ctx context.Context) (map[string]string, error) {
	return s.repo.GetSettings(ctx)
}

func (s *Service) UpdateSettings(ctx context.Context, values map[string]string) error {
	if err := s.repo.UpdateSettings(ctx, values); err != nil {
		s.logger.ErrorContext(ctx, "update settings", "error", err)
		return err
	}
	return nil
}
