package detect

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
)

var t0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func testEngine(t *testing.T, rules []Rule, now time.Time) *Engine {
	t.Helper()
	e := NewEngine(rules, 100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.now = func() time.Time { return now }
	e.started = now.Add(-time.Hour)
	return e
}

func rule(t *testing.T, id string) Rule {
	t.Helper()
	for _, r := range Defaults() {
		if r.ID == id {
			r.Enabled = true
			return r
		}
	}
	t.Fatalf("no built-in rule %q", id)
	return Rule{}
}

func drain(e *Engine) []Detection {
	var out []Detection
	for {
		select {
		case d := <-e.out:
			out = append(out, d)
		default:
			return out
		}
	}
}

func sshFail(ip string, at time.Time) secnorm.Record {
	return secnorm.Record{TS: at, Source: "sshd", Kind: "auth_failure", SrcIP: ip, User: "root", Message: "Failed password for root from " + ip}
}

func TestDefaultsValidate(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Defaults() {
		if err := r.Validate(); err != nil {
			t.Errorf("rule %s: %v", r.ID, err)
		}
		if seen[r.ID] {
			t.Errorf("duplicate rule id %s", r.ID)
		}
		seen[r.ID] = true
	}
}

func TestSSHBruteforceThresholdWindowCooldown(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "ssh-bruteforce")}, t0.Add(2*time.Hour))
	ctx := context.Background()

	var batch []secnorm.Record
	for i := range 29 {
		batch = append(batch, sshFail("203.0.113.9", t0.Add(time.Duration(i)*time.Second)))
	}
	e.ObserveSecurity(ctx, batch)
	if got := drain(e); len(got) != 0 {
		t.Fatalf("29 failures fired %d detections, want 0", len(got))
	}

	e.ObserveSecurity(ctx, []secnorm.Record{sshFail("203.0.113.9", t0.Add(30*time.Second))})
	got := drain(e)
	if len(got) != 1 {
		t.Fatalf("30th failure fired %d detections, want 1", len(got))
	}
	if got[0].Count != 30 || got[0].GroupKey() != "src_ip=203.0.113.9" {
		t.Fatalf("detection = count %d group %q", got[0].Count, got[0].GroupKey())
	}

	// Inside the cooldown: more failures add to the count but do not refire.
	e.ObserveSecurity(ctx, []secnorm.Record{sshFail("203.0.113.9", t0.Add(40*time.Second))})
	if got := drain(e); len(got) != 0 {
		t.Fatalf("failure inside cooldown fired %d detections", len(got))
	}

	// After the cooldown a fresh burst fires again.
	var burst []secnorm.Record
	for i := range 30 {
		burst = append(burst, sshFail("203.0.113.9", t0.Add(61*time.Minute+time.Duration(i)*time.Second)))
	}
	e.ObserveSecurity(ctx, burst)
	if got := drain(e); len(got) != 1 {
		t.Fatalf("burst after cooldown fired %d detections, want 1", len(got))
	}
}

func TestSSHBruteforceSpreadOutDoesNotFire(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "ssh-bruteforce")}, t0.Add(time.Hour))
	var batch []secnorm.Record
	for i := range 40 {
		batch = append(batch, sshFail("203.0.113.9", t0.Add(time.Duration(i)*20*time.Second)))
	}
	e.ObserveSecurity(context.Background(), batch)
	if got := drain(e); len(got) != 0 {
		t.Fatalf("one failure per 20s fired %d detections, want 0 (max 15 in 5m)", len(got))
	}
}

func TestGroupsAreSeparate(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "ssh-bruteforce")}, t0.Add(time.Hour))
	var batch []secnorm.Record
	for i := range 30 {
		ip := "198.51.100.1"
		if i%2 == 0 {
			ip = "198.51.100.2"
		}
		batch = append(batch, sshFail(ip, t0.Add(time.Duration(i)*time.Second)))
	}
	e.ObserveSecurity(context.Background(), batch)
	if got := drain(e); len(got) != 0 {
		t.Fatalf("15 failures each from two IPs fired %d detections", len(got))
	}
}

func TestNonMatchingRecordsIgnored(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "ssh-bruteforce")}, t0.Add(time.Hour))
	var batch []secnorm.Record
	for i := range 40 {
		r := sshFail("203.0.113.9", t0.Add(time.Duration(i)*time.Second))
		r.Kind = "auth_success"
		batch = append(batch, r)
	}
	e.ObserveSecurity(context.Background(), batch)
	if got := drain(e); len(got) != 0 {
		t.Fatalf("successful logins fired the brute-force rule %d times", len(got))
	}
}

func TestK8sSecretRead(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "k8s-secret-read")}, t0.Add(time.Hour))
	read := func(user string) secnorm.Record {
		return secnorm.Record{TS: t0, Source: "k8saudit", Kind: "secrets", Action: "get", User: user}
	}
	e.ObserveSecurity(context.Background(), []secnorm.Record{
		read("system:kube-controller-manager"),
		read("system:node:k3s1"),
		read("system:k3s-supervisor"),
		read("system:serviceaccount:kube-system:helm-traefik"),
		read("system:serviceaccount:default:intruder"),
	})
	got := drain(e)
	if len(got) != 1 || got[0].GroupKey() != "user=system:serviceaccount:default:intruder" {
		t.Fatalf("detections = %+v, want one for the intruder", got)
	}
}

func TestHTTPAuthDeniedMatchesNumericStatus(t *testing.T) {
	r := rule(t, "http-auth-denied")
	r.Threshold = 2
	e := testEngine(t, []Rule{r}, t0.Add(time.Hour))
	rec := secnorm.Record{TS: t0, Source: "traefik", Status: 401, SrcIP: "192.0.2.7"}
	ok := secnorm.Record{TS: t0, Source: "traefik", Status: 200, SrcIP: "192.0.2.7"}
	e.ObserveSecurity(context.Background(), []secnorm.Record{rec, ok, rec})
	if got := drain(e); len(got) != 1 || got[0].Count != 2 {
		t.Fatalf("detections = %+v, want one with count 2", got)
	}
}

func TestDisabledRuleNeverFires(t *testing.T) {
	r := rule(t, "ssh-bruteforce")
	r.Enabled = false
	r.Threshold = 1
	e := testEngine(t, []Rule{r}, t0.Add(time.Hour))
	e.ObserveSecurity(context.Background(), []secnorm.Record{sshFail("203.0.113.9", t0)})
	if got := drain(e); len(got) != 0 {
		t.Fatalf("disabled rule fired %d times", len(got))
	}
}

func TestFutureTimestampCappedAtNow(t *testing.T) {
	r := rule(t, "ssh-bruteforce")
	r.Threshold = 2
	now := t0
	e := testEngine(t, []Rule{r}, now)
	e.ObserveSecurity(context.Background(), []secnorm.Record{
		sshFail("203.0.113.9", now.Add(24*time.Hour)), // skewed clock
		sshFail("203.0.113.9", now),
	})
	if got := drain(e); len(got) != 1 {
		t.Fatalf("a future timestamp hid the second record: %d detections", len(got))
	}
}

func TestLRUCap(t *testing.T) {
	l := newLRU(2)
	l.get("a", time.Minute).add(t0)
	l.get("b", time.Minute).add(t0)
	l.get("a", time.Minute) // a is now most recent
	l.get("c", time.Minute)
	if l.len() != 2 {
		t.Fatalf("len = %d, want 2", l.len())
	}
	if _, ok := l.items["b"]; ok {
		t.Fatal("least recently used key b survived")
	}
	if n := l.get("a", time.Minute).total(); n != 1 {
		t.Fatalf("a lost its count: %d", n)
	}
}

func TestCounterIgnoresEventsOlderThanWindow(t *testing.T) {
	c := newCounter(5 * time.Minute)
	c.add(t0.Add(10 * time.Minute))
	if n := c.add(t0); n != 1 {
		t.Fatalf("an event 10m older than the newest counted: total %d", n)
	}
	if n := c.add(t0.Add(6 * time.Minute)); n != 2 {
		t.Fatalf("an event inside the window: total %d, want 2", n)
	}
}

func sample(host, metric string, at time.Time, v float64) hostmetrics.Sample {
	return hostmetrics.Sample{Host: host, Metric: metric, TS: at, Value: v}
}

func TestDiskFullHoldsForDuration(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "disk-full")}, t0.Add(time.Hour))
	ctx := context.Background()
	for m := range 10 {
		e.ObserveMetrics(ctx, []hostmetrics.Sample{sample("k3s1", "fs./.used_pct", t0.Add(time.Duration(m)*time.Minute), 95)}, nil)
	}
	if got := drain(e); len(got) != 0 {
		t.Fatalf("9 minutes over the limit fired %d detections", len(got))
	}
	e.ObserveMetrics(ctx, []hostmetrics.Sample{sample("k3s1", "fs./.used_pct", t0.Add(10*time.Minute), 96)}, nil)
	got := drain(e)
	if len(got) != 1 || got[0].GroupKey() != "host=k3s1,metric=fs./.used_pct" {
		t.Fatalf("detections = %+v, want one for k3s1 /", got)
	}
}

func TestMetricDipResetsDuration(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "memory-low")}, t0.Add(time.Hour))
	ctx := context.Background()
	for m := range 15 {
		v := 2.0
		if m == 7 {
			v = 30 // recovered for a minute
		}
		e.ObserveMetrics(ctx, []hostmetrics.Sample{sample("h", "mem.avail_pct", t0.Add(time.Duration(m)*time.Minute), v)}, nil)
	}
	if got := drain(e); len(got) != 0 {
		t.Fatalf("a dip at minute 7 should restart the 10m clock; fired %d", len(got))
	}
}

func TestLoadPerCore(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "load-high")}, t0.Add(time.Hour))
	ctx := context.Background()
	feed := func(v float64) {
		for m := range 11 {
			e.ObserveMetrics(ctx, []hostmetrics.Sample{sample("h", "load1", t0.Add(time.Duration(m)*time.Minute), v)}, nil)
		}
	}
	feed(50)
	if got := drain(e); len(got) != 0 {
		t.Fatalf("unknown core count fired %d detections", len(got))
	}
	e.ObserveMetrics(ctx, nil, []hostmetrics.HostInfo{{Host: "h", Cores: 4, LastSeen: t0}})
	feed(7) // below 2 x 4
	if got := drain(e); len(got) != 0 {
		t.Fatalf("load 7 on 4 cores fired %d detections", len(got))
	}
	e.SetRules([]Rule{rule(t, "load-high")})
	feed(9)
	if got := drain(e); len(got) != 1 || got[0].Limit != 8 {
		t.Fatalf("detections = %+v, want one with limit 8", got)
	}
}

func TestHeartbeat(t *testing.T) {
	now := t0
	e := testEngine(t, []Rule{rule(t, "host-silent")}, now)
	e.now = func() time.Time { return now }
	ctx := context.Background()
	e.ObserveMetrics(ctx, nil, []hostmetrics.HostInfo{{Host: "layer7-prod", LastSeen: t0}})

	now = t0.Add(4 * time.Minute)
	e.CheckHeartbeats(ctx)
	if got := drain(e); len(got) != 0 {
		t.Fatalf("4 minutes of silence fired %d detections", len(got))
	}
	now = t0.Add(6 * time.Minute)
	e.CheckHeartbeats(ctx)
	e.CheckHeartbeats(ctx)
	if got := drain(e); len(got) != 1 || got[0].GroupKey() != "host=layer7-prod" {
		t.Fatalf("detections = %+v, want exactly one for layer7-prod", got)
	}

	// Back, then silent again: fires again.
	e.ObserveMetrics(ctx, nil, []hostmetrics.HostInfo{{Host: "layer7-prod", LastSeen: now}})
	now = now.Add(6 * time.Minute)
	e.CheckHeartbeats(ctx)
	if got := drain(e); len(got) != 1 {
		t.Fatalf("second silence fired %d detections, want 1", len(got))
	}
}

func TestHeartbeatGraceAfterStart(t *testing.T) {
	now := t0
	e := testEngine(t, []Rule{rule(t, "host-silent")}, now)
	e.started = now.Add(-2 * time.Minute)
	e.SeedHosts([]hostmetrics.HostInfo{{Host: "k3s2", LastSeen: t0.Add(-time.Hour)}})
	e.CheckHeartbeats(context.Background())
	if got := drain(e); len(got) != 0 {
		t.Fatalf("host silent since before a restart 2m ago fired %d detections", len(got))
	}
	e.now = func() time.Time { return t0.Add(4 * time.Minute) }
	e.CheckHeartbeats(context.Background())
	if got := drain(e); len(got) != 1 {
		t.Fatalf("6m after start the seeded host should fire; got %d", len(got))
	}
}

func TestHeartbeatForgetsRetiredHosts(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "host-silent")}, t0)
	e.started = t0.Add(-30 * 24 * time.Hour)
	e.SeedHosts([]hostmetrics.HostInfo{{Host: "old", LastSeen: t0.Add(-8 * 24 * time.Hour)}})
	e.CheckHeartbeats(context.Background())
	if got := drain(e); len(got) != 0 || len(e.hosts) != 0 {
		t.Fatalf("host silent for 8 days: %d detections, %d hosts tracked", len(got), len(e.hosts))
	}
}

func TestFullQueueDropsWithoutBlocking(t *testing.T) {
	r := rule(t, "ssh-bruteforce")
	r.Threshold, r.Cooldown = 1, 0
	e := NewEngine([]Rule{r}, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.now = func() time.Time { return t0.Add(time.Hour) }
	e.ObserveSecurity(context.Background(), []secnorm.Record{sshFail("a", t0), sshFail("b", t0), sshFail("c", t0)})
	if len(drain(e)) != 1 || e.dropped != 2 {
		t.Fatalf("dropped = %d, want 2 with a queue of 1", e.dropped)
	}
}
