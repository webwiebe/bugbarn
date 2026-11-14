package projects

import (
	"context"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// --- Rename and Merge ---

func (s *Service) Rename(ctx context.Context, oldSlug, newSlug, newName string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Rename",
		trace.WithAttributes(attribute.String("old_slug", oldSlug), attribute.String("new_slug", newSlug)))
	defer span.End()
	if err := s.repo.RenameProject(ctx, oldSlug, newSlug, newName); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "rename project", err, "old_slug", oldSlug, "new_slug", newSlug)
		return err
	}
	s.logger.InfoContext(ctx, "project renamed", "old_slug", oldSlug, "new_slug", newSlug, "new_name", newName)
	return nil
}

// UpdateLimits sets a project's retention window and sampling mode.
func (s *Service) UpdateLimits(ctx context.Context, slug string, retentionDays *int, sampling string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.UpdateLimits",
		trace.WithAttributes(attribute.String("slug", slug), attribute.String("sampling", sampling)))
	defer span.End()
	if err := s.repo.UpdateProjectLimits(ctx, slug, retentionDays, sampling); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "update project limits", err, "slug", slug)
		return err
	}
	days := "inherit"
	if retentionDays != nil {
		days = strconv.Itoa(*retentionDays)
	}
	s.logger.InfoContext(ctx, "project limits updated", "slug", slug, "retention_days", days, "sampling", sampling)
	return nil
}

// SampleAfter reports the deployment-wide sampling threshold, so the API can
// show what a project inheriting the default actually gets.
func (s *Service) SampleAfter() int64 { return s.repo.SampleAfter() }

func (s *Service) Merge(ctx context.Context, sourceSlug, targetSlug string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Merge",
		trace.WithAttributes(attribute.String("source", sourceSlug), attribute.String("target", targetSlug)))
	defer span.End()
	if err := s.repo.MergeProjects(ctx, sourceSlug, targetSlug); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "merge projects", err, "source", sourceSlug, "target", targetSlug)
		return err
	}
	s.usage.invalidate()
	s.logger.InfoContext(ctx, "projects merged", "source", sourceSlug, "target", targetSlug)
	return nil
}
