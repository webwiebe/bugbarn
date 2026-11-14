package projects

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// --- Group operations ---

func (s *Service) CreateGroup(ctx context.Context, name, slug string) (domain.ProjectGroup, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.CreateGroup",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	g, err := s.repo.CreateGroup(ctx, name, slug)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "create group", err, "slug", slug)
		return domain.ProjectGroup{}, err
	}
	s.logger.InfoContext(ctx, "group created", "slug", slug, "id", g.ID)
	return g, nil
}

func (s *Service) ListGroups(ctx context.Context) ([]domain.ProjectGroup, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.ListGroups")
	defer span.End()
	groups, err := s.repo.ListGroups(ctx)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "list groups", err)
	}
	return groups, err
}

func (s *Service) DeleteGroup(ctx context.Context, slug string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.DeleteGroup",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	if err := s.repo.DeleteGroup(ctx, slug); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "delete group", err, "slug", slug)
		return err
	}
	s.logger.InfoContext(ctx, "group deleted", "slug", slug)
	return nil
}

func (s *Service) AssignProjectToGroup(ctx context.Context, projectSlug, groupSlug string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.AssignProjectToGroup",
		trace.WithAttributes(attribute.String("project", projectSlug), attribute.String("group", groupSlug)))
	defer span.End()
	if err := s.repo.AssignProjectToGroup(ctx, projectSlug, groupSlug); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "assign project to group", err, "project", projectSlug, "group", groupSlug)
		return err
	}
	s.logger.InfoContext(ctx, "project assigned to group", "project", projectSlug, "group", groupSlug)
	return nil
}

func (s *Service) RemoveProjectFromGroup(ctx context.Context, projectSlug string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.RemoveProjectFromGroup",
		trace.WithAttributes(attribute.String("project", projectSlug)))
	defer span.End()
	if err := s.repo.RemoveProjectFromGroup(ctx, projectSlug); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "remove project from group", err, "project", projectSlug)
		return err
	}
	s.logger.InfoContext(ctx, "project removed from group", "project", projectSlug)
	return nil
}

func (s *Service) ListGroupProjects(ctx context.Context, groupSlug string) ([]domain.Project, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.ListGroupProjects",
		trace.WithAttributes(attribute.String("group", groupSlug)))
	defer span.End()
	projects, err := s.repo.ListGroupProjects(ctx, groupSlug)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "list group projects", err)
	}
	return projects, err
}
