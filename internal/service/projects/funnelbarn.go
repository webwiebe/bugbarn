package projects

import (
	"context"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// maxFunnelBarnURLLen bounds the stored link.
const maxFunnelBarnURLLen = 2048

// UpdateFunnelBarnURL sets the FunnelBarn page a project's By surface rows link
// to. Empty clears it. The value must be an absolute http or https URL without
// credentials; "{surface}" in it is substituted per row by the UI.
func (s *Service) UpdateFunnelBarnURL(ctx context.Context, slug, raw string) (string, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.UpdateFunnelBarnURL",
		trace.WithAttributes(attribute.String("slug", slug)))
	defer span.End()
	link := strings.TrimSpace(raw)
	if link != "" {
		if err := validateFunnelBarnURL(link); err != nil {
			return "", err
		}
	}
	if err := s.repo.UpdateProjectFunnelBarnURL(ctx, slug, link); err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "update project funnelbarn url", "slug", slug, "error", err)
		return "", err
	}
	s.logger.InfoContext(ctx, "project funnelbarn url updated", "slug", slug, "set", link != "")
	return link, nil
}

func validateFunnelBarnURL(link string) error {
	if len(link) > maxFunnelBarnURLLen {
		return apperr.InvalidInput("funnelbarn_url is too long", nil)
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return apperr.InvalidInput("funnelbarn_url must be an absolute http or https URL without credentials", nil)
	}
	return nil
}
