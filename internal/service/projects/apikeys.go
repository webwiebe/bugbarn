package projects

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

func (s *Service) ListAPIKeys(ctx context.Context) ([]domain.APIKey, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.ListAPIKeys")
	defer span.End()
	keys, err := s.repo.ListAPIKeys(ctx)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "list api keys", err)
	}
	return keys, err
}

func (s *Service) CreateAPIKey(ctx context.Context, name string, projectID int64, keySHA256, scope string) (domain.APIKey, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.CreateAPIKey")
	defer span.End()
	key, err := s.repo.CreateAPIKey(ctx, name, projectID, keySHA256, scope)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "create api key", err, "name", name)
		return domain.APIKey{}, err
	}
	s.logger.InfoContext(ctx, "api key created", "name", name, "project_id", projectID)
	return key, nil
}

func (s *Service) DeleteAPIKey(ctx context.Context, id int64) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.DeleteAPIKey",
		trace.WithAttributes(attribute.Int64("api_key_id", id)))
	defer span.End()
	if err := s.repo.DeleteAPIKey(ctx, id); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "delete api key", err, "id", id)
		return err
	}
	return nil
}

func (s *Service) EnsureSetupAPIKey(ctx context.Context, name string, projectID int64, keySHA256 string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.EnsureSetupAPIKey")
	defer span.End()
	if err := s.repo.EnsureSetupAPIKey(ctx, name, projectID, keySHA256); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "ensure setup api key", err)
		return err
	}
	return nil
}

func (s *Service) ValidAPIKeySHA256(ctx context.Context, keySHA256 string) (projectID int64, scope string, found bool, err error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.ValidAPIKeySHA256")
	defer span.End()
	projectID, scope, found, err = s.repo.ValidAPIKeySHA256(ctx, keySHA256)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "validate api key", err)
	}
	return
}

func (s *Service) TouchAPIKey(ctx context.Context, keySHA256 string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.TouchAPIKey")
	defer span.End()
	if err := s.repo.TouchAPIKey(ctx, keySHA256); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "touch api key", err)
		return err
	}
	return nil
}
