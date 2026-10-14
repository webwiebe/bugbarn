package timeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type fakeRepo struct {
	rows  []storage.TimelineRow
	err   error
	calls int
	since time.Time
}

func (f *fakeRepo) ErrorTimelineRows(_ context.Context, since time.Time) ([]storage.TimelineRow, error) {
	f.calls++
	f.since = since
	return f.rows, f.err
}

func newService(repo *fakeRepo, now time.Time) *Service {
	s := New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return now }
	return s
}

func hour(day, h int) time.Time { return time.Date(2026, 10, day, h, 0, 0, 0, time.UTC) }

func TestErrorTimeline24hZeroFillsHourlyBuckets(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC)
	repo := &fakeRepo{rows: []storage.TimelineRow{
		{ProjectSlug: "alpha", ProjectName: "Alpha", Hour: hour(14, 10), Count: 4},
		{ProjectSlug: "beta", ProjectName: "Beta", Hour: hour(14, 10), Count: 1},
		{ProjectSlug: "alpha", ProjectName: "Alpha", Hour: hour(13, 12), Count: 2},
	}}
	got, err := newService(repo, now).ErrorTimeline(context.Background(), "")
	if err != nil {
		t.Fatalf("ErrorTimeline: %v", err)
	}
	if got.Range != "24h" || got.BucketSeconds != 3600 || len(got.Buckets) != 24 {
		t.Fatalf("shape = %s %d %d buckets", got.Range, got.BucketSeconds, len(got.Buckets))
	}
	if !got.Buckets[0].Start.Equal(hour(13, 11)) || !got.Buckets[23].Start.Equal(hour(14, 10)) {
		t.Errorf("window = %s .. %s", got.Buckets[0].Start, got.Buckets[23].Start)
	}
	if !repo.since.Equal(hour(13, 11)) {
		t.Errorf("queried since %s, want %s", repo.since, hour(13, 11))
	}
	if c := got.Buckets[23].Counts; c["alpha"] != 4 || c["beta"] != 1 {
		t.Errorf("current bucket = %v", c)
	}
	if c := got.Buckets[1].Counts; c["alpha"] != 2 {
		t.Errorf("13:12 bucket = %v", c)
	}
	if len(got.Buckets[5].Counts) != 0 {
		t.Errorf("empty bucket has counts %v", got.Buckets[5].Counts)
	}
	if len(got.Projects) != 2 || got.Projects[0].Slug != "alpha" || got.Projects[0].Total != 6 || got.Projects[1].Total != 1 {
		t.Errorf("projects = %+v", got.Projects)
	}
}

func TestErrorTimeline7dFoldsHoursIntoSixHourBuckets(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC)
	repo := &fakeRepo{rows: []storage.TimelineRow{
		{ProjectSlug: "alpha", Hour: hour(14, 6), Count: 1},
		{ProjectSlug: "alpha", Hour: hour(14, 9), Count: 2},
		{ProjectSlug: "alpha", Hour: hour(14, 5), Count: 5},
	}}
	got, err := newService(repo, now).ErrorTimeline(context.Background(), "7d")
	if err != nil {
		t.Fatalf("ErrorTimeline: %v", err)
	}
	if len(got.Buckets) != 28 || got.BucketSeconds != 6*3600 {
		t.Fatalf("shape = %d buckets, %d s", len(got.Buckets), got.BucketSeconds)
	}
	last := got.Buckets[27]
	if !last.Start.Equal(hour(14, 6)) || last.Counts["alpha"] != 3 {
		t.Errorf("last bucket = %s %v, want 06:00 with 3", last.Start, last.Counts)
	}
	if prev := got.Buckets[26]; !prev.Start.Equal(hour(14, 0)) || prev.Counts["alpha"] != 5 {
		t.Errorf("previous bucket = %s %v, want 00:00 with 5", prev.Start, prev.Counts)
	}
}

func TestErrorTimeline30dUsesDailyBuckets(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC)
	repo := &fakeRepo{rows: []storage.TimelineRow{
		{ProjectSlug: "alpha", Hour: hour(14, 1), Count: 1},
		{ProjectSlug: "alpha", Hour: hour(14, 23), Count: 1},
	}}
	got, err := newService(repo, now).ErrorTimeline(context.Background(), "30d")
	if err != nil {
		t.Fatalf("ErrorTimeline: %v", err)
	}
	if len(got.Buckets) != 30 || got.BucketSeconds != 86400 {
		t.Fatalf("shape = %d buckets, %d s", len(got.Buckets), got.BucketSeconds)
	}
	if got.Buckets[29].Counts["alpha"] != 2 {
		t.Errorf("today = %v", got.Buckets[29].Counts)
	}
}

func TestErrorTimelineRejectsUnknownRange(t *testing.T) {
	_, err := newService(&fakeRepo{}, time.Now()).ErrorTimeline(context.Background(), "90d")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
}

func TestErrorTimelineCachesAndServesStaleOnFailure(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC)
	repo := &fakeRepo{rows: []storage.TimelineRow{{ProjectSlug: "alpha", Hour: hour(14, 10), Count: 1}}}
	svc := newService(repo, now)
	ctx := context.Background()
	if _, err := svc.ErrorTimeline(ctx, "24h"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.ErrorTimeline(ctx, "24h"); err != nil || repo.calls != 1 {
		t.Fatalf("second call: err=%v calls=%d, want a cache hit", err, repo.calls)
	}
	later := now.Add(time.Minute)
	svc.now = func() time.Time { return later }
	repo.err = errors.New("db down")
	got, err := svc.ErrorTimeline(ctx, "24h")
	if err != nil || repo.calls != 2 || got.Projects[0].Slug != "alpha" {
		t.Fatalf("stale fallback: %v %+v", err, got)
	}
	if _, err := svc.ErrorTimeline(ctx, "7d"); err == nil {
		t.Fatal("a failure with nothing cached must return the error")
	}
}
