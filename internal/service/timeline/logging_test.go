package timeline

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

func TestLoggingRule(t *testing.T) {
	logtest.CheckRule(t, []logtest.Call{
		{Name: "ErrorTimeline", Run: func(log *slog.Logger, err error) {
			s := New(&fakeRepo{err: err}, log)
			s.now = func() time.Time { return time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC) }
			_, _ = s.ErrorTimeline(context.Background(), "24h")
		}},
	})
}
