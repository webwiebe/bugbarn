package writeforward

import (
	"context"
	"time"
)

// Flush forwards everything still in the spool before the reader exits, so the
// next reader version does not take over with this backlog on disk. A failed
// pass (the writer restarting, say) is retried every retry. It returns nil once
// the spool is empty, or the last forward error when ctx ends first; the caller
// bounds ctx so a writer that stays down cannot block pod termination.
//
// Flush must not run alongside Drain: stop the background loop first.
func (s *Spool) Flush(ctx context.Context, retry time.Duration) error {
	for {
		err := s.DrainOnce(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(retry):
		}
	}
}
