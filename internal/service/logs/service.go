package logs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

type Repository interface {
	InsertLogEntries(context.Context, []domain.LogEntry) error
	ListLogEntries(ctx context.Context, projectID int64, levelMin int, query string, limit int, beforeID int64) ([]domain.LogEntry, error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "logs")}
}

func (s *Service) Insert(ctx context.Context, entries []domain.LogEntry) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.logs.Insert",
		trace.WithAttributes(attribute.Int("count", len(entries))))
	defer span.End()

	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}
		if err = s.repo.InsertLogEntries(ctx, entries); err == nil {
			if attempt > 0 {
				span.SetAttributes(attribute.Int("retry.attempts", attempt))
			}
			return nil
		}
		if !storage.IsDatabaseLocked(err) {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(time.Duration(100*(1<<attempt)) * time.Millisecond):
		}
	}
	if errors.Is(err, context.Canceled) {
		// Client disconnected mid-insert; this is not a server error worth
		// alerting on (and selflog would otherwise capture it as one).
		s.logger.InfoContext(ctx, "insert log entries canceled", "count", len(entries))
		return err
	}
	span.SetStatus(codes.Error, err.Error())
	s.logger.ErrorContext(ctx, "insert log entries", "count", len(entries), "error", err)
	return err
}

func (s *Service) List(ctx context.Context, projectID int64, levelMin int, query string, limit int, beforeID int64) ([]domain.LogEntry, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.logs.List")
	defer span.End()
	entries, err := s.repo.ListLogEntries(ctx, projectID, levelMin, query, limit, beforeID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// Client disconnected mid-query; not a server error worth alerting on.
			s.logger.InfoContext(ctx, "list log entries canceled", "project_id", projectID)
			return nil, err
		}
		span.SetStatus(codes.Error, err.Error())
		s.logger.ErrorContext(ctx, "list log entries", "project_id", projectID, "error", err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("count", len(entries)))
	return entries, nil
}
