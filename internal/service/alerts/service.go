package alerts

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Repository defines the data access contract for alert operations.
type Repository interface {
	ListAlerts(context.Context) ([]domain.Alert, error)
	GetAlert(context.Context, string) (domain.Alert, error)
	CreateAlert(context.Context, domain.Alert) (domain.Alert, error)
	UpdateAlert(context.Context, string, domain.Alert) (domain.Alert, error)
	DeleteAlert(context.Context, string) error
}

type Service struct {
	repo Repository
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]domain.Alert, error) {
	return s.repo.ListAlerts(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (domain.Alert, error) {
	return s.repo.GetAlert(ctx, id)
}

func (s *Service) Create(ctx context.Context, alert domain.Alert) (domain.Alert, error) {
	return s.repo.CreateAlert(ctx, alert)
}

func (s *Service) Update(ctx context.Context, id string, alert domain.Alert) (domain.Alert, error) {
	return s.repo.UpdateAlert(ctx, id, alert)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.DeleteAlert(ctx, id)
}
