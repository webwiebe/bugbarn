package releases

import (
	"context"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

func TestLoggingRule(t *testing.T) {
	ctx := context.Background()
	run := func(f func(*Service)) func(*slog.Logger, error) {
		return func(log *slog.Logger, err error) { f(New(&fakeRepo{err: err}, log)) }
	}
	logtest.CheckRule(t, []logtest.Call{
		{Name: "List", Run: run(func(s *Service) { _, _ = s.List(ctx) })},
		{Name: "Get", Run: run(func(s *Service) { _, _ = s.Get(ctx, "r") })},
		{Name: "Create", Run: run(func(s *Service) { _, _ = s.Create(ctx, domain.Release{Name: "n"}) })},
		{Name: "Update", Run: run(func(s *Service) { _, _ = s.Update(ctx, "r", domain.Release{}) })},
		{Name: "Delete", Run: run(func(s *Service) { _ = s.Delete(ctx, "r") })},
		{Name: "UploadSourceMap", Run: run(func(s *Service) { _, _ = s.UploadSourceMap(ctx, domain.SourceMapUpload{}) })},
		{Name: "ListSourceMaps", Run: run(func(s *Service) { _, _ = s.ListSourceMaps(ctx) })},
	})
}
