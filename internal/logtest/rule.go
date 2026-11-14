package logtest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// Call runs one service method against a repository that fails with err, using
// log as the service's logger.
type Call struct {
	Name string
	Run  func(log *slog.Logger, err error)
}

// CheckRule asserts the service logging rule for every call: client errors and
// context cancellation write no ERROR record, any other failure writes exactly
// one.
func CheckRule(t *testing.T, calls []Call) {
	t.Helper()
	client := map[string]error{
		"not found":     apperr.NotFound("missing", nil),
		"conflict":      apperr.Conflict("duplicate", nil),
		"invalid input": apperr.InvalidInput("bad", nil),
		"canceled":      fmt.Errorf("query: %w", context.Canceled),
		"deadline":      context.DeadlineExceeded,
	}
	internal := map[string]error{
		"internal": apperr.Internal("disk", errors.New("disk I/O error")),
		"plain":    errors.New("boom"),
	}
	for _, c := range calls {
		for kind, err := range client {
			log, rec := New()
			c.Run(log, err)
			if n := len(rec.Errors()); n != 0 {
				t.Errorf("%s with %s wrote %d ERROR records, want 0", c.Name, kind, n)
			}
		}
		for kind, err := range internal {
			log, rec := New()
			c.Run(log, err)
			if n := len(rec.Errors()); n != 1 {
				t.Errorf("%s with %s wrote %d ERROR records, want 1", c.Name, kind, n)
			}
		}
	}
}
