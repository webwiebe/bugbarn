package projects

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// --- Alias operations ---

func (s *Service) CreateAlias(ctx context.Context, aliasSlug string, projectID int64) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.CreateAlias",
		trace.WithAttributes(attribute.String("alias", aliasSlug), attribute.Int64("project_id", projectID)))
	defer span.End()
	if err := s.repo.CreateAlias(ctx, aliasSlug, projectID); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "create alias", err, "alias", aliasSlug, "project_id", projectID)
		return err
	}
	s.logger.InfoContext(ctx, "alias created", "alias", aliasSlug, "project_id", projectID)
	return nil
}

func (s *Service) DeleteAlias(ctx context.Context, aliasSlug string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.DeleteAlias",
		trace.WithAttributes(attribute.String("alias", aliasSlug)))
	defer span.End()
	if err := s.repo.DeleteAlias(ctx, aliasSlug); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "delete alias", err, "alias", aliasSlug)
		return err
	}
	s.logger.InfoContext(ctx, "alias deleted", "alias", aliasSlug)
	return nil
}

func (s *Service) ResolveAlias(ctx context.Context, slug string) (int64, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.ResolveAlias",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	id, err := s.repo.ResolveAlias(ctx, slug)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "resolve alias", err)
	}
	return id, err
}

func (s *Service) ListAliases(ctx context.Context) ([]domain.ProjectAlias, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.ListAliases")
	defer span.End()
	aliases, err := s.repo.ListAliases(ctx)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "list aliases", err)
	}
	return aliases, err
}
