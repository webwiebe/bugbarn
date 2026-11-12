package projects

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// SilenceWatch returns whether the silence rules judge a project: "" when they
// do, "off" when the project opted out.
func (s *Service) SilenceWatch(ctx context.Context, slug string) (string, error) {
	return s.repo.ProjectSilenceWatch(ctx, slug)
}

// UpdateSilenceWatch sets whether the silence rules judge a project. The value
// is "" (watched) or "off" (a test project, or an app shut down on purpose).
func (s *Service) UpdateSilenceWatch(ctx context.Context, slug, raw string) (string, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.UpdateSilenceWatch",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	value := strings.TrimSpace(raw)
	if err := s.repo.UpdateProjectSilenceWatch(ctx, slug, value); err != nil {
		tracing.SetError(span, err)
		s.logger.ErrorContext(ctx, "update project silence watch", "slug", slug, "error", err)
		return "", err
	}
	s.logger.InfoContext(ctx, "project silence watch updated", "slug", slug, "silence_watch", value)
	return value, nil
}
