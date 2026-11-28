package projects

import (
	"context"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
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
	return s.MergeInto(ctx, sourceSlug, targetSlug, "")
}

// MergeInto merges the source project into the target and, when environment is
// not empty, stamps it on the source rows that name none. The name must be
// letters, digits, dots, underscores or hyphens, at most 64 bytes.
func (s *Service) MergeInto(ctx context.Context, sourceSlug, targetSlug, environment string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Merge",
		trace.WithAttributes(attribute.String("source", sourceSlug), attribute.String("target", targetSlug),
			attribute.String("environment", environment)))
	defer span.End()
	if !validEnvironmentName(environment) {
		return apperr.InvalidInput("environment must be 1 to 64 letters, digits, dots, underscores or hyphens", nil)
	}
	var err error
	if environment == "" {
		err = s.repo.MergeProjects(ctx, sourceSlug, targetSlug)
	} else {
		err = s.repo.MergeProjectsAs(ctx, sourceSlug, targetSlug, environment)
	}
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "merge projects", err, "source", sourceSlug, "target", targetSlug)
		return err
	}
	s.usage.invalidate()
	s.logger.InfoContext(ctx, "projects merged", "source", sourceSlug, "target", targetSlug, "environment", environment)
	return nil
}

// MergeReport merges like MergeInto and returns what moved. With dryRun it
// changes nothing and returns what the merge would move. A source that already
// is an alias of the target returns an already-merged report, so a re-run is safe.
func (s *Service) MergeReport(ctx context.Context, sourceSlug, targetSlug, environment string,
	dryRun bool) (domain.MergeReport, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.MergeReport",
		trace.WithAttributes(attribute.String("source", sourceSlug), attribute.String("target", targetSlug),
			attribute.String("environment", environment), attribute.Bool("dry_run", dryRun)))
	defer span.End()
	if !validEnvironmentName(environment) {
		return domain.MergeReport{}, apperr.InvalidInput("environment must be 1 to 64 letters, digits, dots, underscores or hyphens", nil)
	}
	rep, err := s.repo.MergeProjectsReport(ctx, sourceSlug, targetSlug, environment, dryRun)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "merge projects", err, "source", sourceSlug, "target", targetSlug)
		return rep, err
	}
	if dryRun || rep.AlreadyMerged {
		return rep, nil
	}
	s.usage.invalidate()
	s.logger.InfoContext(ctx, "projects merged", "source", sourceSlug, "target", targetSlug, "environment", environment)
	return rep, nil
}

// validEnvironmentName reports whether name is empty (no stamping) or a plain
// environment name.
func validEnvironmentName(name string) bool {
	if name == "" {
		return true
	}
	if len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
