package writeforward

import (
	"context"
	"time"
)

// FlushBeforeExit forwards what the spool still holds when the reader stops,
// so the next reader version does not take over with this backlog on disk. It
// waits at most budget, so a writer that stays down cannot block pod
// termination, and logs the outcome.
//
// It must not run alongside Drain: stop the background loop first.
func (s *Spool) FlushBeforeExit(budget, retry time.Duration) {
	pending := s.Pending()
	if pending == 0 {
		return
	}
	s.logger.Info("draining ingest spool before exit", "pending", pending)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	if err := s.Flush(ctx, retry); err != nil {
		s.logger.Warn("ingest spool drain incomplete", "error", err, "remaining", s.Pending())
		return
	}
	s.logger.Info("ingest spool drained")
}

// Flush forwards everything in the spool. A failed pass (the writer
// restarting, say) is retried every retry. It returns nil once the spool is
// empty, or the last forward error when ctx ends first.
func (s *Spool) Flush(ctx context.Context, retry time.Duration) error {
	for {
		err := s.DrainOnce(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(retry):
		}
	}
}
