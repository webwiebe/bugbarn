package logs

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Repository defines the data access contract for log entry operations.
type Repository interface {
	InsertLogEntries(context.Context, []domain.LogEntry) error
	ListLogEntries(ctx context.Context, projectID int64, levelMin int, query string, limit int, beforeID int64) ([]domain.LogEntry, error)
}

type Service struct {
	repo Repository
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Insert(ctx context.Context, entries []domain.LogEntry) error {
	return s.repo.InsertLogEntries(ctx, entries)
}

func (s *Service) List(ctx context.Context, projectID int64, levelMin int, query string, limit int, beforeID int64) ([]domain.LogEntry, error) {
	return s.repo.ListLogEntries(ctx, projectID, levelMin, query, limit, beforeID)
}
