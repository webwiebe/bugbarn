package alerts

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
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
	ctx, span := tracing.Tracer().Start(ctx, "service.alerts.List")
	defer span.End()
	alerts, err := s.repo.ListAlerts(ctx)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "list alerts", err)
	}
	return alerts, err
}

func (s *Service) Get(ctx context.Context, id string) (domain.Alert, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.alerts.Get",
		trace.WithAttributes(attribute.String("alert_id", id)))
	defer span.End()
	alert, err := s.repo.GetAlert(ctx, id)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "get alert", err, "alert_id", id)
		return domain.Alert{}, err
	}
	return alert, nil
}

func (s *Service) Create(ctx context.Context, alert domain.Alert) (domain.Alert, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.alerts.Create")
	defer span.End()
	created, err := s.repo.CreateAlert(ctx, alert)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "create alert", err, "name", alert.Name)
		return domain.Alert{}, err
	}
	s.logger.InfoContext(ctx, "alert created", "alert_id", created.ID, "name", created.Name)
	return created, nil
}

func (s *Service) Update(ctx context.Context, id string, alert domain.Alert) (domain.Alert, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.alerts.Update",
		trace.WithAttributes(attribute.String("alert_id", id)))
	defer span.End()
	updated, err := s.repo.UpdateAlert(ctx, id, alert)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "update alert", err, "alert_id", id)
		return domain.Alert{}, err
	}
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.alerts.Delete",
		trace.WithAttributes(attribute.String("alert_id", id)))
	defer span.End()
	if err := s.repo.DeleteAlert(ctx, id); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "delete alert", err, "alert_id", id)
		return err
	}
	s.logger.InfoContext(ctx, "alert deleted", "alert_id", id)
	return nil
}
