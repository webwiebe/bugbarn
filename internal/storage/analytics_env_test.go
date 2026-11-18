package storage

import (
	"context"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

func seedEnvViews(t *testing.T, s *Store, pid int64) analytics.Query {
	t.Helper()
	now := time.Now().UTC()
	prod := map[string]string{"environment": "production", "plan": "pro"}
	stg := map[string]string{"environment": "staging", "plan": "free"}
	views := []analytics.PageView{
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "p1", ReferrerHost: "a.example", ExitPathname: "/pricing", MaxScrollPct: 80, InteractionCount: 1, DurationMs: 4000, Props: prod},
		{ProjectID: pid, Ts: now.Add(-time.Second), Pathname: "/pricing", SessionID: "p1", ReferrerHost: "a.example", Props: prod},
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "p2", ReferrerHost: "b.example", Props: prod},
		{ProjectID: pid, Ts: now, Pathname: "/home", SessionID: "s1", ReferrerHost: "c.example", Props: stg},
		{ProjectID: pid, Ts: now, Pathname: "/docs", SessionID: "n1"},
	}
	for _, pv := range views {
		if err := s.InsertPageView(context.Background(), pv); err != nil {
			t.Fatalf("InsertPageView: %v", err)
		}
	}
	return analytics.Query{ProjectID: pid, Start: now.Add(-24 * time.Hour), End: now}
}

func TestEnvironmentFilterNarrowsEveryAnalyticsRead(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	q := seedEnvViews(t, s, s.DefaultProjectID())
	q.Environment = "production"

	ov, err := s.QueryOverview(ctx, q)
	if err != nil || ov.Pageviews != 3 || ov.Sessions != 2 || ov.PagesCount != 2 {
		t.Fatalf("overview = %+v, %v; want 3 views, 2 sessions, 2 pages", ov, err)
	}
	pages, err := s.QueryPages(ctx, q)
	if err != nil || len(pages) != 2 || pages[0].Pathname != "/home" || pages[0].Pageviews != 2 {
		t.Fatalf("pages = %+v, %v", pages, err)
	}
	refs, err := s.QueryReferrers(ctx, q)
	if err != nil || len(refs) != 2 || refs[0].Host != "a.example" || refs[0].Pageviews != 2 {
		t.Fatalf("referrers = %+v, %v", refs, err)
	}
	for _, g := range []string{"day", "week", "month"} {
		tl, err := s.QueryTimeline(ctx, q, g)
		if err != nil || len(tl) == 0 || tl[0].Pageviews != 3 {
			t.Fatalf("timeline %s = %+v, %v", g, tl, err)
		}
	}
	seg, err := s.QuerySegments(ctx, q, "plan")
	if err != nil || len(seg) != 1 || seg[0].Value != "pro" {
		t.Fatalf("segments = %+v, %v; want only the production plan", seg, err)
	}
	flow, err := s.QueryPageFlow(ctx, q, "/home")
	if err != nil || len(flow.WentTo) != 1 || flow.WentTo[0].Pathname != "/pricing" {
		t.Fatalf("flow = %+v, %v", flow, err)
	}
	scroll, err := s.QueryScrollDepth(ctx, q, "/home")
	var n int64
	for _, b := range scroll.Buckets {
		n += b.Count
	}
	if err != nil || n != 2 {
		t.Fatalf("scroll counted %d views, %v; want 2", n, err)
	}
	drop, err := s.QueryDropout(ctx, q)
	var dn int64
	for _, d := range drop {
		dn += d.Pageviews
	}
	if err != nil || dn != 3 {
		t.Fatalf("dropout counted %d views, %v; want 3", dn, err)
	}
}

func TestEnvironmentFilterCameFromFollowsTheEnvironment(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	q := seedEnvViews(t, s, s.DefaultProjectID())
	q.Environment = "production"
	flow, err := s.QueryPageFlow(ctx, q, "/home")
	if err != nil {
		t.Fatal(err)
	}
	// /pricing was viewed one second before /home inside session p1.
	if len(flow.CameFrom) != 1 || flow.CameFrom[0].Pathname != "/pricing" {
		t.Fatalf("cameFrom = %+v", flow.CameFrom)
	}
	q.Environment = "staging"
	if flow, err = s.QueryPageFlow(ctx, q, "/home"); err != nil || len(flow.CameFrom) != 0 {
		t.Fatalf("staging cameFrom = %+v, %v; want none", flow.CameFrom, err)
	}
}

func TestEmptyEnvironmentKeepsTheRollupPath(t *testing.T) {
	s := mustOpenStore(t)
	q := seedEnvViews(t, s, s.DefaultProjectID())
	ov, err := s.QueryOverview(context.Background(), q)
	if err != nil || ov.Pageviews != 5 {
		t.Fatalf("overview = %+v, %v; want all 5 views", ov, err)
	}
}
