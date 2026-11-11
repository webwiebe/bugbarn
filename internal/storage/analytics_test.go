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

func TestQueryScrollDepth(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	now := time.Now().UTC()
	views := []analytics.PageView{
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s1", MaxScrollPct: 10},
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s2", MaxScrollPct: 40},
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s3", MaxScrollPct: 60},
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s4", MaxScrollPct: 80},
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s5", MaxScrollPct: 100},
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
	result, err := s.QueryScrollDepth(ctx, q, "/home")
	if err != nil {
		t.Fatalf("QueryScrollDepth: %v", err)
	}
	if result.Pathname != "/home" {
		t.Errorf("expected pathname /home, got %s", result.Pathname)
	}
	if len(result.Buckets) == 0 {
		t.Fatal("expected scroll buckets, got none")
	}
	// Each of the 5 views lands in a different bucket.
	total := int64(0)
	for _, b := range result.Buckets {
		total += b.Count
	}
	if total != 5 {
		t.Errorf("expected 5 total scroll entries, got %d", total)
	}
}

func TestQueryPageFlow(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	now := time.Now().UTC()
	// Session: /home -> /about -> /contact
	views := []analytics.PageView{
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s1"},
		{ProjectID: pid, Ts: now.Add(time.Second), Pathname: "/about", SessionID: "s1"},
		{ProjectID: pid, Ts: now.Add(2 * time.Second), Pathname: "/contact", SessionID: "s1"},
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
	flow, err := s.QueryPageFlow(ctx, q, "/about")
	if err != nil {
		t.Fatalf("QueryPageFlow: %v", err)
	}
	if len(flow.CameFrom) == 0 {
		t.Fatal("expected at least one CameFrom entry")
	}
	if flow.CameFrom[0].Pathname != "/home" {
		t.Errorf("expected CameFrom /home, got %s", flow.CameFrom[0].Pathname)
	}
}

func TestQueryDropout(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	now := time.Now().UTC()
	views := []analytics.PageView{
		// bounced (interaction_count=0)
		{ProjectID: pid, Ts: now, Pathname: "/landing", SessionID: "s1", InteractionCount: 0},
		{ProjectID: pid, Ts: now, Pathname: "/landing", SessionID: "s2", InteractionCount: 0},
		// not bounced
		{ProjectID: pid, Ts: now, Pathname: "/landing", SessionID: "s3", InteractionCount: 3},
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
	stats, err := s.QueryDropout(ctx, q)
	if err != nil {
		t.Fatalf("QueryDropout: %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("expected dropout stats, got none")
	}
	found := false
	for _, st := range stats {
		if st.Pathname == "/landing" {
			found = true
			if st.Pageviews != 3 {
				t.Errorf("expected 3 pageviews for /landing, got %d", st.Pageviews)
			}
			if st.BouncedSessions != 2 {
				t.Errorf("expected 2 bounced sessions, got %d", st.BouncedSessions)
			}
			expectedRate := 2.0 / 3.0
			if st.BounceRate < expectedRate-0.01 || st.BounceRate > expectedRate+0.01 {
				t.Errorf("expected bounce rate ~%.3f, got %.3f", expectedRate, st.BounceRate)
			}
		}
	}
	if !found {
		t.Error("expected /landing in dropout stats")
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

// QueryPages and QueryReferrers share one top-N read over analytics_daily. Both
// order by pageviews descending, honour the limit, and leave the other kind of
// row (totals, other dimensions) out.
func TestQueryPagesAndReferrersTopN(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()

	rows := []struct {
		pathname, dimKey, dimValue string
		pv, sess                   int
	}{
		{"", "", "", 60, 20},                           // daily total: neither list
		{"/a", "", "", 10, 4},                          // page
		{"/b", "", "", 30, 9},                          // page, the busiest
		{"/c", "", "", 20, 6},                          // page
		{"", "referrer_host", "news.example", 5, 2},    // referrer
		{"", "referrer_host", "search.example", 25, 8}, // referrer, the busiest
		{"", "utm_source", "mail", 99, 50},             // other dimension: neither list
	}
	for _, r := range rows {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO analytics_daily (project_id, date, pathname, dim_key, dim_value, pageviews, sessions)
			VALUES (?, '2026-11-10', ?, ?, ?, ?, ?)`,
			pid, r.pathname, r.dimKey, r.dimValue, r.pv, r.sess); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	q := analytics.Query{ProjectID: pid, Start: day, End: day, Limit: 2}

	pages, err := s.QueryPages(ctx, q)
	if err != nil {
		t.Fatalf("QueryPages: %v", err)
	}
	if len(pages) != 2 || pages[0].Pathname != "/b" || pages[0].Pageviews != 30 || pages[1].Pathname != "/c" {
		t.Errorf("pages = %+v, want /b then /c", pages)
	}

	refs, err := s.QueryReferrers(ctx, q)
	if err != nil {
		t.Fatalf("QueryReferrers: %v", err)
	}
	if len(refs) != 2 || refs[0].Host != "search.example" || refs[0].Sessions != 8 || refs[1].Host != "news.example" {
		t.Errorf("referrers = %+v, want search.example then news.example", refs)
	}

	empty := analytics.Query{ProjectID: pid, Start: day.AddDate(0, 0, -30), End: day.AddDate(0, 0, -29)}
	if got, err := s.QueryPages(ctx, empty); err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty range pages = %#v, %v; want empty non-nil slice", got, err)
	}
}
