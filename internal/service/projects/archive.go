package projects

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// ArchiveQuietPeriod is how long a project must have received nothing before it
// can be archived.
const ArchiveQuietPeriod = 7 * 24 * time.Hour

// Archive marks a project archived once it has been quiet for the quiet period
// and returns the time of its last event, log line or page view (zero when it
// never had one). The last activity time goes to the log, so the archive leaves
// a record of when the sender stopped. The data stays readable.
func (s *Service) Archive(ctx context.Context, slug string) (time.Time, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Archive",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	last, err := s.repo.ArchiveProject(ctx, slug, ArchiveQuietPeriod)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "archive project", err, "slug", slug)
		return last, err
	}
	s.logger.InfoContext(ctx, "project archived", "slug", slug, "last_event_at", formatLast(last))
	return last, nil
}

// Unarchive returns an archived project to active.
func (s *Service) Unarchive(ctx context.Context, slug string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.Unarchive",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	if err := s.repo.UnarchiveProject(ctx, slug); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "unarchive project", err, "slug", slug)
		return err
	}
	s.logger.InfoContext(ctx, "project unarchived", "slug", slug)
	return nil
}

func formatLast(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format(time.RFC3339)
}
