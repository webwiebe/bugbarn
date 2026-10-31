package projects

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// APIKey returns one API key row, without the plaintext key.
func (s *Service) APIKey(ctx context.Context, id int64) (domain.APIKey, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.APIKey",
		trace.WithAttributes(attribute.Int64("api_key_id", id)))
	defer span.End()
	key, err := s.repo.APIKeyByID(ctx, id)
	if err != nil {
		tracing.SetError(span, err)
	}
	return key, err
}

// RevokeSetupKey ends the derived setup key of slug for good. keySHA256 is that
// key's hash, so its row goes in the same step when there is one.
func (s *Service) RevokeSetupKey(ctx context.Context, slug, keySHA256 string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.RevokeSetupKey")
	defer span.End()
	if err := s.repo.RevokeSetupKey(ctx, slug, keySHA256); err != nil {
		tracing.SetError(span, err)
		s.logger.ErrorContext(ctx, "revoke setup key", "slug", slug, "error", err)
		return err
	}
	s.logger.InfoContext(ctx, "setup key revoked", "slug", slug)
	return nil
}

// ListSetupKeyRevocations returns every revoked setup key.
func (s *Service) ListSetupKeyRevocations(ctx context.Context) ([]domain.SetupKeyRevocation, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.ListSetupKeyRevocations")
	defer span.End()
	revoked, err := s.repo.ListSetupKeyRevocations(ctx)
	if err != nil {
		tracing.SetError(span, err)
	}
	return revoked, err
}
