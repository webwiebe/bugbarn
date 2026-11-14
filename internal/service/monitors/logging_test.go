package monitors

import (
	"context"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

// failingRepo fails the reads and the delete that fakeRepo answers with success.
type failingRepo struct {
	fakeRepo
	failure error
}

func (r *failingRepo) ListEnabledMonitors(context.Context) ([]domain.Monitor, error) {
	return nil, r.failure
}
func (r *failingRepo) DeleteMonitor(context.Context, int64, string) error { return r.failure }
func (r *failingRepo) ListMonitors(context.Context, int64) ([]domain.Monitor, error) {
	return nil, r.failure
}

func TestLoggingRule(t *testing.T) {
	ctx := context.Background()
	run := func(f func(*Service)) func(*slog.Logger, error) {
		return func(log *slog.Logger, err error) { f(New(&failingRepo{failure: err}, log)) }
	}
	logtest.CheckRule(t, []logtest.Call{
		{Name: "ListAll", Run: run(func(s *Service) { _, _ = s.ListAll(ctx) })},
		{Name: "List", Run: run(func(s *Service) { _, _ = s.List(ctx, 1) })},
		{Name: "Delete", Run: run(func(s *Service) { _ = s.Delete(ctx, 1, "m") })},
	})
}
