package issues

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

func TestLoggingRule(t *testing.T) {
	ctx := context.Background()
	run := func(f func(*Service)) func(*slog.Logger, error) {
		return func(log *slog.Logger, err error) { f(New(&fakeRepo{err: err, countsErr: err}, log)) }
	}
	logtest.CheckRule(t, []logtest.Call{
		{Name: "List", Run: run(func(s *Service) { _, _ = s.List(ctx) })},
		{Name: "ListFiltered", Run: run(func(s *Service) { _, _ = s.ListFiltered(ctx, domain.IssueFilter{}) })},
		{Name: "Get", Run: run(func(s *Service) { _, _ = s.Get(ctx, "i") })},
		{Name: "Resolve", Run: run(func(s *Service) { _, _ = s.Resolve(ctx, "i") })},
		{Name: "Reopen", Run: run(func(s *Service) { _, _ = s.Reopen(ctx, "i") })},
		{Name: "Mute", Run: run(func(s *Service) { _, _ = s.Mute(ctx, "i", "forever") })},
		{Name: "Unmute", Run: run(func(s *Service) { _, _ = s.Unmute(ctx, "i") })},
		{Name: "HourlyEventCounts", Run: run(func(s *Service) { _, _ = s.HourlyEventCounts(ctx, []int64{1}) })},
		{Name: "RowIDByDisplayID", Run: run(func(s *Service) { _, _ = s.RowIDByDisplayID(ctx, "BB-1") })},
		{Name: "ListEvents", Run: run(func(s *Service) { _, _, _ = s.ListEvents(ctx, "i", 10, 0) })},
		{Name: "IssueReleases", Run: run(func(s *Service) { _, _, _ = s.IssueReleases(ctx, "i") })},
		{Name: "GetEvent", Run: run(func(s *Service) { _, _ = s.GetEvent(ctx, "e") })},
		{Name: "ListLiveEvents", Run: run(func(s *Service) { _, _ = s.ListLiveEvents(ctx, 10, time.Now()) })},
		{Name: "ListFacetKeys", Run: run(func(s *Service) { _, _ = s.ListFacetKeys(ctx, 1) })},
		{Name: "SurfaceActivity", Run: run(func(s *Service) { _, _ = s.SurfaceActivity(ctx, 1) })},
		{Name: "ListFacetValues", Run: run(func(s *Service) { _, _ = s.ListFacetValues(ctx, 1, "k") })},
		{Name: "Counts", Run: run(func(s *Service) { _, _ = s.Counts(ctx) })},
	})
}
