package analytics

import (
	"context"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

func TestLoggingRule(t *testing.T) {
	ctx := context.Background()
	q := analytics.Query{}
	run := func(f func(*Service)) func(*slog.Logger, error) {
		return func(log *slog.Logger, err error) { f(New(&fakeRepo{err: err}, log)) }
	}
	logtest.CheckRule(t, []logtest.Call{
		{Name: "InsertPageView", Run: run(func(s *Service) { _ = s.InsertPageView(ctx, analytics.PageView{}) })},
		{Name: "QueryOverview", Run: run(func(s *Service) { _, _ = s.QueryOverview(ctx, q) })},
		{Name: "QueryPages", Run: run(func(s *Service) { _, _ = s.QueryPages(ctx, q) })},
		{Name: "QueryTimeline", Run: run(func(s *Service) { _, _ = s.QueryTimeline(ctx, q, "day") })},
		{Name: "QueryReferrers", Run: run(func(s *Service) { _, _ = s.QueryReferrers(ctx, q) })},
		{Name: "QuerySegments", Run: run(func(s *Service) { _, _ = s.QuerySegments(ctx, q, "browser") })},
		{Name: "QueryPageFlow", Run: run(func(s *Service) { _, _ = s.QueryPageFlow(ctx, q, "/") })},
		{Name: "QueryScrollDepth", Run: run(func(s *Service) { _, _ = s.QueryScrollDepth(ctx, q, "/") })},
		{Name: "QueryDropout", Run: run(func(s *Service) { _, _ = s.QueryDropout(ctx, q) })},
	})
}
