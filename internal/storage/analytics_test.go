package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

func mustOpenStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertPageViewAndQueryOverview(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	now := time.Now().UTC()
	views := []analytics.PageView{
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s1", DurationMs: 5000},
		{ProjectID: pid, Ts: now, Pathname: "/about", SessionID: "s1", DurationMs: 3000},
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s2", DurationMs: 2000},
	}
	for _, pv := range views {
		if err := s.InsertPageView(ctx, pv); err != nil {
			t.Fatalf("InsertPageView: %v", err)
		}
	}

	q := analytics.Query{
		ProjectID: pid,
		Start:     now.Add(-time.Hour),
		End:       now.Add(time.Hour),
	}
	res, err := s.QueryOverview(ctx, q)
	if err != nil {
		t.Fatalf("QueryOverview: %v", err)
	}
	if res.Pageviews != 3 {
		t.Errorf("expected 3 pageviews, got %d", res.Pageviews)
	}
	if res.Sessions != 2 {
		t.Errorf("expected 2 sessions, got %d", res.Sessions)
	}
}

func TestRollupDailyAnalytics(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	for i := 0; i < 5; i++ {
		sid := "sess-a"
		if i >= 3 {
			sid = "sess-b"
		}
		if err := s.InsertPageView(ctx, analytics.PageView{
			ProjectID: pid,
			Ts:        yesterday.Add(time.Duration(i) * time.Hour),
			Pathname:  "/page",
			SessionID: sid,
		}); err != nil {
			t.Fatalf("InsertPageView: %v", err)
		}
	}

	if err := s.RollupDailyAnalytics(ctx, pid, yesterday); err != nil {
		t.Fatalf("RollupDailyAnalytics: %v", err)
	}

	// Second call must be idempotent.
	if err := s.RollupDailyAnalytics(ctx, pid, yesterday); err != nil {
		t.Fatalf("RollupDailyAnalytics (idempotent): %v", err)
	}

	q := analytics.Query{
		ProjectID: pid,
		Start:     yesterday,
		End:       yesterday.Add(23 * time.Hour),
	}
	res, err := s.QueryOverview(ctx, q)
	if err != nil {
		t.Fatalf("QueryOverview after rollup: %v", err)
	}
	if res.Pageviews != 5 {
		t.Errorf("expected 5 pageviews, got %d", res.Pageviews)
	}
	if res.Sessions != 2 {
		t.Errorf("expected 2 sessions, got %d", res.Sessions)
	}
}

func TestDeleteOldPageviews(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	old := time.Now().UTC().AddDate(0, 0, -100)
	recent := time.Now().UTC() // today, visible to QueryOverview's raw-today path
	for _, ts := range []time.Time{old, recent} {
		if err := s.InsertPageView(ctx, analytics.PageView{
			ProjectID: pid, Ts: ts, Pathname: "/x", SessionID: "s",
		}); err != nil {
			t.Fatalf("InsertPageView: %v", err)
		}
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	if err := s.DeleteOldPageviews(ctx, cutoff); err != nil {
		t.Fatalf("DeleteOldPageviews: %v", err)
	}

	q := analytics.Query{ProjectID: pid, Start: old.Add(-time.Hour), End: time.Now()}
	res, err := s.QueryOverview(ctx, q)
	if err != nil {
		t.Fatalf("QueryOverview: %v", err)
	}
	if res.Pageviews != 1 {
		t.Errorf("expected 1 remaining pageview, got %d", res.Pageviews)
	}
}

func TestQueryTimeline(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	// Insert two days of data via rollup
	d1 := time.Now().UTC().AddDate(0, 0, -3).Truncate(24 * time.Hour)
	d2 := time.Now().UTC().AddDate(0, 0, -2).Truncate(24 * time.Hour)
	for _, ts := range []time.Time{d1, d2} {
		if err := s.InsertPageView(ctx, analytics.PageView{
			ProjectID: pid, Ts: ts, Pathname: "/x", SessionID: "s",
		}); err != nil {
			t.Fatalf("InsertPageView: %v", err)
		}
		if err := s.RollupDailyAnalytics(ctx, pid, ts); err != nil {
			t.Fatalf("RollupDailyAnalytics: %v", err)
		}
	}

	q := analytics.Query{ProjectID: pid, Start: d1, End: d2.Add(23 * time.Hour)}
	buckets, err := s.QueryTimeline(ctx, q, "day")
	if err != nil {
		t.Fatalf("QueryTimeline: %v", err)
	}
	if len(buckets) != 2 {
		t.Errorf("expected 2 timeline buckets, got %d", len(buckets))
	}
}

func TestQuerySegments(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	now := time.Now().UTC()
	views := []analytics.PageView{
		{ProjectID: pid, Ts: now, Pathname: "/", SessionID: "s1", Props: map[string]string{"plan": "pro"}},
		{ProjectID: pid, Ts: now, Pathname: "/", SessionID: "s2", Props: map[string]string{"plan": "pro"}},
		{ProjectID: pid, Ts: now, Pathname: "/", SessionID: "s3", Props: map[string]string{"plan": "free"}},
	}
	for _, pv := range views {
		if err := s.InsertPageView(ctx, pv); err != nil {
			t.Fatalf("InsertPageView: %v", err)
		}
	}

	q := analytics.Query{ProjectID: pid, Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	buckets, err := s.QuerySegments(ctx, q, "plan")
	if err != nil {
		t.Fatalf("QuerySegments: %v", err)
	}
	if len(buckets) != 2 {
		t.Errorf("expected 2 segment buckets, got %d", len(buckets))
	}
	if buckets[0].Value != "pro" || buckets[0].Pageviews != 2 {
		t.Errorf("unexpected first bucket: %+v", buckets[0])
	}
}
