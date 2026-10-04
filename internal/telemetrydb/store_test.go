package telemetrydb

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
)

var t0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func sshRecords(n int, ip string) []secnorm.Record {
	recs := make([]secnorm.Record, n)
	for i := range recs {
		recs[i] = secnorm.Record{
			TS: t0.Add(time.Duration(i) * time.Second), Source: "syslog", Host: "mini1",
			Kind: "auth_failure", SrcIP: ip, User: "root", Action: "sshd",
			Message: fmt.Sprintf("Failed password for root from %s port %d", ip, 4000+i), Raw: "{}",
		}
	}
	return recs
}

func TestInsertAndSearchSecurity(t *testing.T) {
	ctx := context.Background()
	d := openTest(t, Security, 0)
	// 1200 rows crosses the 500-row insert chunk twice.
	recs := append(sshRecords(1200, "203.0.113.9"), secnorm.Record{
		TS: t0, Source: "traefik", Host: "k3s1", Kind: "http", SrcIP: "198.51.100.4",
		Status: 401, Method: "POST", Path: "/api/v1/100%_login", Message: "POST /api/v1/100%_login 401",
	})
	if err := d.InsertSecurity(ctx, recs, t0); err != nil {
		t.Fatal(err)
	}
	window := SecurityQuery{From: t0.Add(-time.Hour), To: t0.Add(time.Hour)}

	q := window
	q.SrcIP, q.Limit = "203.0.113.9", 500
	page1, err := d.SearchSecurity(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 500 || page1[0].ID <= page1[1].ID || page1[0].User != "root" {
		t.Fatalf("page1: %d rows, first %+v", len(page1), page1[0])
	}
	q.Before = page1[len(page1)-1].ID
	page2, err := d.SearchSecurity(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 500 || page2[0].ID >= q.Before {
		t.Fatalf("page2: %d rows starting at %d, cursor %d", len(page2), page2[0].ID, q.Before)
	}

	cases := []struct {
		name string
		mod  func(*SecurityQuery)
		want int
	}{
		{"status", func(q *SecurityQuery) { q.Status = 401 }, 1},
		{"source and host", func(q *SecurityQuery) { q.Source, q.Host = "traefik", "k3s1" }, 1},
		{"user", func(q *SecurityQuery) { q.User, q.Limit = "root", 500 }, 500},
		{"text matches LIKE metacharacters literally", func(q *SecurityQuery) { q.Text = "100%_login" }, 1},
		{"text does not treat _ as a wildcard", func(q *SecurityQuery) { q.Text = "100%xlogin" }, 0},
		{"time window", func(q *SecurityQuery) { q.From, q.To = t0.Add(10*time.Second), t0.Add(19*time.Second) }, 10},
	}
	for _, c := range cases {
		q := window
		c.mod(&q)
		rows, err := d.SearchSecurity(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(rows) != c.want {
			t.Errorf("%s: %d rows, want %d", c.name, len(rows), c.want)
		}
	}
}

func TestInsertSecurityRefusedWhenFull(t *testing.T) {
	d := openTest(t, Security, 0)
	d.full.Store(true)
	if err := d.InsertSecurity(context.Background(), sshRecords(1, "203.0.113.9"), t0); !errors.Is(err, ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
}

func TestInsertSecurityWithMetaAdvancesCursorWhenFull(t *testing.T) {
	ctx := context.Background()
	d := openTest(t, Security, 0)
	if v, err := d.Meta(ctx, "cf:zone"); err != nil || v != "" {
		t.Fatalf("unset meta = %q, %v", v, err)
	}
	if err := d.InsertSecurityWithMeta(ctx, sshRecords(3, "203.0.113.9"), t0, "cf:zone", "c1"); err != nil {
		t.Fatal(err)
	}
	d.full.Store(true)
	if err := d.InsertSecurityWithMeta(ctx, sshRecords(3, "203.0.113.9"), t0, "cf:zone", "c2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Meta(ctx, "cf:zone"); v != "c2" {
		t.Fatalf("cursor = %q, want c2", v)
	}
	rows, err := d.SearchSecurity(ctx, SecurityQuery{From: t0.Add(-time.Hour), To: t0.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3 (the full file skips the second batch)", len(rows))
	}
}

func TestInsertMetricsAndQuery(t *testing.T) {
	ctx := context.Background()
	d := openTest(t, Metrics, 0)
	samples := []hostmetrics.Sample{
		{Host: "mini1", Metric: "load1", TS: t0, Value: 1.5},
		{Host: "mini1", Metric: "load1", TS: t0.Add(time.Minute), Value: 2.5},
		{Host: "mini1", Metric: "mem.avail_pct", TS: t0, Value: 40},
		{Host: "k3s1", Metric: "load1", TS: t0, Value: 0.2},
	}
	hosts := []hostmetrics.HostInfo{{Host: "mini1", Cores: 10, LastSeen: t0.Add(time.Minute)}, {Host: "k3s1", Cores: 8, LastSeen: t0}}
	if err := d.InsertMetrics(ctx, samples, hosts); err != nil {
		t.Fatal(err)
	}
	// A redelivered sample overwrites; a later batch without cores keeps them,
	// and an older last_seen never moves the host back in time.
	if err := d.InsertMetrics(ctx,
		[]hostmetrics.Sample{{Host: "mini1", Metric: "load1", TS: t0, Value: 3}},
		[]hostmetrics.HostInfo{{Host: "mini1", LastSeen: t0}}); err != nil {
		t.Fatal(err)
	}

	got, err := d.Hosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("hosts = %+v", got)
	}
	for _, h := range got {
		if h.Host == "mini1" && (h.Cores != 10 || !h.LastSeen.Equal(t0.Add(time.Minute))) {
			t.Fatalf("mini1 = %+v, want cores 10 and last_seen kept", h)
		}
	}

	series, err := d.Series(ctx, "mini1", "load1", t0, t0.Add(time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 || series[0].Value != 3 || series[1].Value != 2.5 {
		t.Fatalf("series = %+v", series)
	}
	if hourly, err := d.Series(ctx, "mini1", "load1", t0, t0.Add(time.Hour), true); err != nil || len(hourly) != 0 {
		t.Fatalf("hourly = %+v, %v (nothing rolled up yet)", hourly, err)
	}

	names, err := d.Metrics(ctx, "mini1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(names) != "[load1 mem.avail_pct]" {
		t.Fatalf("metrics = %v", names)
	}
}

// A full metrics file drops samples but keeps host last_seen current, or the
// heartbeat rule would report every host as silent.
func TestInsertMetricsWhenFullKeepsHosts(t *testing.T) {
	ctx := context.Background()
	d := openTest(t, Metrics, 0)
	d.full.Store(true)
	err := d.InsertMetrics(ctx, []hostmetrics.Sample{{Host: "h", Metric: "load1", TS: t0}},
		[]hostmetrics.HostInfo{{Host: "h", Cores: 4, LastSeen: t0}})
	if !errors.Is(err, ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	if hosts, _ := d.Hosts(ctx); len(hosts) != 1 || !hosts[0].LastSeen.Equal(t0) {
		t.Fatalf("hosts = %+v, want h seen at t0", hosts)
	}
	if series, _ := d.Series(ctx, "h", "load1", t0, t0, false); len(series) != 0 {
		t.Fatalf("series = %+v, want no samples", series)
	}
}
