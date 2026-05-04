package logs

import (
	"context"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
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
	if err := s.repo.InsertLogEntries(ctx, entries); err != nil {
		s.logger.ErrorContext(ctx, "insert log entries", "count", len(entries), "error", err)
		return err
	}
	return nil
}

func (s *Service) List(ctx context.Context, projectID int64, levelMin int, query string, limit int, beforeID int64) ([]domain.LogEntry, error) {
	entries, err := s.repo.ListLogEntries(ctx, projectID, levelMin, query, limit, beforeID)
	if err != nil {
		s.logger.ErrorContext(ctx, "list log entries", "project_id", projectID, "error", err)
		return nil, err
	}
	return entries, nil
}
