package analytics

import (
	"context"
	"errors"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

type fakeRepo struct {
	err error

	inserted    analytics.PageView
	gotQuery    analytics.Query
	gotArg      string
	overview    analytics.OverviewResult
	pages       []analytics.PageStat
	timeline    []analytics.TimelineBucket
	referrers   []analytics.ReferrerStat
	segments    []analytics.SegmentBucket
	flow        analytics.PageFlowResult
	scroll      analytics.ScrollDepthResult
	dropout     []analytics.DropoutStat
	insertCalls int
}

func (f *fakeRepo) InsertPageView(_ context.Context, pv analytics.PageView) error {
	f.insertCalls++
	f.inserted = pv
	return f.err
}

func (f *fakeRepo) QueryOverview(_ context.Context, q analytics.Query) (analytics.OverviewResult, error) {
	f.gotQuery = q
	return f.overview, f.err
}

func (f *fakeRepo) QueryPages(_ context.Context, q analytics.Query) ([]analytics.PageStat, error) {
	f.gotQuery = q
	return f.pages, f.err
}

func (f *fakeRepo) QueryTimeline(_ context.Context, q analytics.Query, g string) ([]analytics.TimelineBucket, error) {
	f.gotQuery, f.gotArg = q, g
	return f.timeline, f.err
}

func (f *fakeRepo) QueryReferrers(_ context.Context, q analytics.Query) ([]analytics.ReferrerStat, error) {
	f.gotQuery = q
	return f.referrers, f.err
}

func (f *fakeRepo) QuerySegments(_ context.Context, q analytics.Query, k string) ([]analytics.SegmentBucket, error) {
	f.gotQuery, f.gotArg = q, k
	return f.segments, f.err
}

func (f *fakeRepo) QueryPageFlow(_ context.Context, q analytics.Query, p string) (analytics.PageFlowResult, error) {
	f.gotQuery, f.gotArg = q, p
	return f.flow, f.err
}

func (f *fakeRepo) QueryScrollDepth(_ context.Context, q analytics.Query, p string) (analytics.ScrollDepthResult, error) {
	f.gotQuery, f.gotArg = q, p
	return f.scroll, f.err
}

func (f *fakeRepo) QueryDropout(_ context.Context, q analytics.Query) ([]analytics.DropoutStat, error) {
	f.gotQuery = q
	return f.dropout, f.err
}

func TestNewDefaultsLogger(t *testing.T) {
	if svc := New(&fakeRepo{}, nil); svc.logger == nil {
		t.Fatal("expected a default logger")
	}
}

func TestQueriesReturnRepoResults(t *testing.T) {
	repo := &fakeRepo{
		overview:  analytics.OverviewResult{Pageviews: 7},
		pages:     []analytics.PageStat{{Pathname: "/a"}},
		timeline:  []analytics.TimelineBucket{{Date: "2026-01-01"}},
		referrers: []analytics.ReferrerStat{{Host: "example.com"}},
		segments:  []analytics.SegmentBucket{{Value: "mobile"}},
		flow:      analytics.PageFlowResult{Pathname: "/a"},
		scroll:    analytics.ScrollDepthResult{Pathname: "/a"},
		dropout:   []analytics.DropoutStat{{Pathname: "/a"}},
	}
	svc := New(repo, nil)
	ctx := context.Background()
	q := analytics.Query{ProjectID: 42, Limit: 5}

	if err := svc.InsertPageView(ctx, analytics.PageView{ProjectID: 42, Pathname: "/a"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if repo.inserted.Pathname != "/a" || repo.insertCalls != 1 {
		t.Fatalf("page view not forwarded: %+v", repo.inserted)
	}
	if got, err := svc.QueryOverview(ctx, q); err != nil || got.Pageviews != 7 {
		t.Fatalf("overview = %+v, %v", got, err)
	}
	if got, err := svc.QueryPages(ctx, q); err != nil || len(got) != 1 {
		t.Fatalf("pages = %+v, %v", got, err)
	}
	if got, err := svc.QueryTimeline(ctx, q, "day"); err != nil || len(got) != 1 || repo.gotArg != "day" {
		t.Fatalf("timeline = %+v, %v, arg %q", got, err, repo.gotArg)
	}
	if got, err := svc.QueryReferrers(ctx, q); err != nil || got[0].Host != "example.com" {
		t.Fatalf("referrers = %+v, %v", got, err)
	}
	if got, err := svc.QuerySegments(ctx, q, "device"); err != nil || got[0].Value != "mobile" || repo.gotArg != "device" {
		t.Fatalf("segments = %+v, %v", got, err)
	}
	if got, err := svc.QueryPageFlow(ctx, q, "/a"); err != nil || got.Pathname != "/a" || repo.gotArg != "/a" {
		t.Fatalf("flow = %+v, %v", got, err)
	}
	if got, err := svc.QueryScrollDepth(ctx, q, "/b"); err != nil || got.Pathname != "/a" || repo.gotArg != "/b" {
		t.Fatalf("scroll = %+v, %v", got, err)
	}
	if got, err := svc.QueryDropout(ctx, q); err != nil || got[0].Pathname != "/a" {
		t.Fatalf("dropout = %+v, %v", got, err)
	}
	if repo.gotQuery.ProjectID != 42 || repo.gotQuery.Limit != 5 {
		t.Fatalf("query not forwarded: %+v", repo.gotQuery)
	}
}

func TestRepoErrorsPassThrough(t *testing.T) {
	boom := errors.New("boom")
	svc := New(&fakeRepo{err: boom}, nil)
	ctx := context.Background()
	q := analytics.Query{}

	checks := map[string]error{
		"insert": svc.InsertPageView(ctx, analytics.PageView{}),
	}
	_, checks["overview"] = svc.QueryOverview(ctx, q)
	_, checks["pages"] = svc.QueryPages(ctx, q)
	_, checks["timeline"] = svc.QueryTimeline(ctx, q, "day")
	_, checks["referrers"] = svc.QueryReferrers(ctx, q)
	_, checks["segments"] = svc.QuerySegments(ctx, q, "k")
	_, checks["flow"] = svc.QueryPageFlow(ctx, q, "/")
	_, checks["scroll"] = svc.QueryScrollDepth(ctx, q, "/")
	_, checks["dropout"] = svc.QueryDropout(ctx, q)
	for name, err := range checks {
		if !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the repo error", name, err)
		}
	}
}
