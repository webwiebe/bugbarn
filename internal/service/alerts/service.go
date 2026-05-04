package alerts

import (
	"context"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type Repository interface {
	ListAlerts(context.Context) ([]domain.Alert, error)
	GetAlert(context.Context, string) (domain.Alert, error)
	CreateAlert(context.Context, domain.Alert) (domain.Alert, error)
	UpdateAlert(context.Context, string, domain.Alert) (domain.Alert, error)
	DeleteAlert(context.Context, string) error
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "alerts")}
}

func (s *Service) List(ctx context.Context) ([]domain.Alert, error) {
	return s.repo.ListAlerts(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (domain.Alert, error) {
	alert, err := s.repo.GetAlert(ctx, id)
	if err != nil {
		s.logger.ErrorContext(ctx, "get alert", "alert_id", id, "error", err)
		return domain.Alert{}, err
	}
	return alert, nil
}

func (s *Service) Create(ctx context.Context, alert domain.Alert) (domain.Alert, error) {
	created, err := s.repo.CreateAlert(ctx, alert)
	if err != nil {
		s.logger.ErrorContext(ctx, "create alert", "name", alert.Name, "error", err)
		return domain.Alert{}, err
	}
	s.logger.InfoContext(ctx, "alert created", "alert_id", created.ID, "name", created.Name)
	return created, nil
}

func (s *Service) Update(ctx context.Context, id string, alert domain.Alert) (domain.Alert, error) {
	updated, err := s.repo.UpdateAlert(ctx, id, alert)
	if err != nil {
		s.logger.ErrorContext(ctx, "update alert", "alert_id", id, "error", err)
		return domain.Alert{}, err
	}
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.repo.DeleteAlert(ctx, id); err != nil {
		s.logger.ErrorContext(ctx, "delete alert", "alert_id", id, "error", err)
		return err
	}
	s.logger.InfoContext(ctx, "alert deleted", "alert_id", id)
	return nil
}
