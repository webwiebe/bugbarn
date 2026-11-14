package projects

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// silenceWatchOff opts a project out of the silence rules. The storage layer
// checks the same value as a guard.
const silenceWatchOff = "off"

// SilenceWatch returns whether the silence rules judge a project: "" when they
// do, "off" when the project opted out.
func (s *Service) SilenceWatch(ctx context.Context, slug string) (string, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.SilenceWatch",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	value, err := s.repo.ProjectSilenceWatch(ctx, slug)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "read project silence watch", err, "slug", slug)
	}
	return value, err
}

// UpdateSilenceWatch sets whether the silence rules judge a project. The value
// is "" (watched) or "off" (a test project, or an app shut down on purpose).
func (s *Service) UpdateSilenceWatch(ctx context.Context, slug, raw string) (string, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.UpdateSilenceWatch",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	value := strings.TrimSpace(raw)
	if value != "" && value != silenceWatchOff {
		err := apperr.InvalidInput("silence_watch must be empty or \"off\"", nil)
		tracing.SetError(span, err)
		return "", err
	}
	if err := s.repo.UpdateProjectSilenceWatch(ctx, slug, value); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "update project silence watch", err, "slug", slug)
		return "", err
	}
	s.logger.InfoContext(ctx, "project silence watch updated", "slug", slug, "silence_watch", value)
	return value, nil
}
