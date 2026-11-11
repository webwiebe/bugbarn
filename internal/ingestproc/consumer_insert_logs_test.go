package ingestproc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type scriptedLogs struct {
	errs  []error
	calls int
}

func (l *scriptedLogs) Insert(context.Context, []domain.LogEntry) error {
	i := l.calls
	l.calls++
	if i < len(l.errs) {
		return l.errs[i]
	}
	return nil
}

func insertLogsOutcome(ctx context.Context, logs *scriptedLogs) string {
	c := NewConsumer(nil, nil, logs, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return c.insertLogs(ctx, "proj", []domain.LogEntry{{Message: "x"}})
}

// insertLogs labels the outcome of inserting parsed log entries: success, a
// retry after a transient error, a drop on a permanent error, and a drop when
// the context ends during a transient failure.
func TestInsertLogsOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		logs := &scriptedLogs{}
		if got := insertLogsOutcome(context.Background(), logs); got != "success" || logs.calls != 1 {
			t.Fatalf("outcome %q after %d calls, want success after 1", got, logs.calls)
		}
	})
	t.Run("transient error retried once", func(t *testing.T) {
		t.Parallel()
		logs := &scriptedLogs{errs: []error{context.DeadlineExceeded}}
		if got := insertLogsOutcome(context.Background(), logs); got != "success" || logs.calls != 2 {
			t.Fatalf("outcome %q after %d calls, want success after 2", got, logs.calls)
		}
	})
	t.Run("permanent error is dropped", func(t *testing.T) {
		t.Parallel()
		logs := &scriptedLogs{errs: []error{errors.New("constraint failed")}}
		if got := insertLogsOutcome(context.Background(), logs); got != "insert_error" || logs.calls != 1 {
			t.Fatalf("outcome %q after %d calls, want insert_error after 1", got, logs.calls)
		}
	})
	t.Run("canceled context during a transient error", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		logs := &scriptedLogs{errs: []error{context.DeadlineExceeded}}
		if got := insertLogsOutcome(ctx, logs); got != "transient_drop" || logs.calls != 1 {
			t.Fatalf("outcome %q after %d calls, want transient_drop after 1", got, logs.calls)
		}
	})
}
