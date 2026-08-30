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

// insertMaxAttempts bounds how many times one Insert call re-tries a batch that
// storage bounced off the contended writer.
const insertMaxAttempts = 5

// insertRetryBudget bounds the wall-clock cost of those attempts. A single
// attempt can sit in SQLite's busy handler for up to busy_timeout (10s) or burn
// the storage layer's own per-batch ceiling, and neither is interruptible by
// the Go context, so without a budget the attempts can occupy the single writer
// connection for well over a minute. Both callers retry the whole batch on
// their own backoff, so giving up early only defers the insert.
const insertRetryBudget = 20 * time.Second

// contended reports whether a failed insert is worth another attempt.
//
// Storage caps every batch with its own deadline so one insert cannot
// monopolize the shared writer connection, and SQLite's busy handler is not
// context-interruptible. Write-lock contention that outlasts that cap therefore
// arrives here as context.DeadlineExceeded rather than SQLITE_BUSY: the same
// condition wearing a different error, and just as retryable. Matching only
// SQLITE_BUSY made this retry loop give up on the first attempt for the very
// case it exists to ride out. Mirrors ingestproc.isTransientPersistError, which
// the callers already classify by.
func contended(err error) bool {
	return storage.IsDatabaseLocked(err) || errors.Is(err, context.DeadlineExceeded)
}

func (s *Service) Insert(ctx context.Context, entries []domain.LogEntry) error {
	ctx, span := tracing.Tracer().Start(ctx, "service.logs.Insert",
		trace.WithAttributes(attribute.Int("count", len(entries))))
	defer span.End()

	budget := time.Now().Add(insertRetryBudget)
	var err error
	for attempt := 0; attempt < insertMaxAttempts; attempt++ {
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
		// Nothing to wait for after the final attempt: sleeping there would only
		// hold the batch (and the consumer's write mutex) for no gain.
		if !contended(err) || attempt == insertMaxAttempts-1 || !time.Now().Before(budget) {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(time.Duration(100*(1<<attempt)) * time.Millisecond):
		}
	}
	return s.reportInsertErr(ctx, span, err, len(entries))
}

// reportInsertErr logs a failed insert at the level its disposition deserves and
// returns it unchanged. selflog turns every error-level log into an issue
// against us, so only a failure that actually loses data may log at that level;
// the batch is intact in both softer cases below, and whoever holds it decides
// whether losing it is worth an error (Consumer.persistLog and
// Replayer.ReplayHeld both do, once their own retries are spent).
func (s *Service) reportInsertErr(ctx context.Context, span trace.Span, err error, count int) error {
	switch {
	case errors.Is(err, context.Canceled), ctx.Err() != nil:
		// Client disconnected or the writer is shutting down mid-insert; the
		// caller requeues or abandons the batch deliberately.
		s.logger.InfoContext(ctx, "insert log entries canceled", "count", count)
		return err
	case contended(err):
		// The writer was busy for longer than we are willing to hold the batch.
		// Nothing is lost yet — the caller retries — so this is back-pressure,
		// not a fault. Reporting it as one is what filed BS2-83 6866 times.
		span.SetStatus(codes.Error, err.Error())
		s.logger.WarnContext(ctx, "insert log entries deferred, writer contended", "count", count, "error", err)
		return err
	}
	span.SetStatus(codes.Error, err.Error())
	s.logger.ErrorContext(ctx, "insert log entries", "count", count, "error", err)
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
