package projects

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

func (s *Service) GetSettings(ctx context.Context) (map[string]string, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.GetSettings")
	defer span.End()
	result, err := s.repo.GetSettings(ctx)
	if err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "get settings", err)
	}
	return result, err
}

func (s *Service) UpdateSettings(ctx context.Context, values map[string]string) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.UpdateSettings")
	defer span.End()
	if err := s.repo.UpdateSettings(ctx, values); err != nil {
		tracing.SetError(span, err)
		apperr.LogFailure(ctx, s.logger, "update settings", err)
		return err
	}
	return nil
}
