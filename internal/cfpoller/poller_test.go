package cfpoller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
	"github.com/wiebe-xyz/bugbarn/internal/telemetry"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

const (
	zone  = "zone-a"
	token = "test-token"
)

var now = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// fakeCF serves firewallEventsAdaptive from an in-memory list, applying the
// datetime_geq/datetime_leq filter, datetime_ASC order and the limit the
// way Cloudflare does.
type fakeCF struct {
	mu       sync.Mutex
	events   []map[string]any
	status   int    // non-zero: answer with this HTTP status
	gqlError string // non-empty: answer with a GraphQL error
	requests []map[string]string
}

var limitRe = regexp.MustCompile(`limit: (\d+)`)

func (f *fakeCF) add(ray string, ts time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, map[string]any{
		"action": "block", "clientIP": "203.0.113.7", "clientCountryName": "NL",
		"clientRequestHTTPHost": "wiebe.xyz", "clientRequestHTTPMethodName": "GET",
		"clientRequestPath": "/wp-login.php", "datetime": ts.UTC().Format(time.RFC3339),
		"edgeResponseStatus": 403, "rayName": ray, "ruleId": "r1", "source": "waf",
	})
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Query     string `json:"query"`
		Variables struct {
			ZoneTag string            `json:"zoneTag"`
			Filter  map[string]string `json:"filter"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.requests = append(f.requests, req.Variables.Filter)
	if f.status != 0 {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(f.status)
		return
	}
	if f.gqlError != "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []map[string]string{{"message": f.gqlError}}})
		return
	}
	limit, _ := strconv.Atoi(limitRe.FindStringSubmatch(req.Query)[1])
	since, _ := time.Parse(time.RFC3339, req.Variables.Filter["datetime_geq"])
	until, _ := time.Parse(time.RFC3339, req.Variables.Filter["datetime_leq"])
	var out []map[string]any
	for _, e := range f.events {
		ts, _ := time.Parse(time.RFC3339, e["datetime"].(string))
		if !ts.Before(since) && !ts.After(until) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i]["datetime"].(string) < out[j]["datetime"].(string) })
	if len(out) > limit {
		out = out[:limit]
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{"viewer": map[string]any{"zones": []any{map[string]any{"firewallEventsAdaptive": out}}}},
	})
}

type recordingObserver struct{ recs []secnorm.Record }

func (o *recordingObserver) ObserveSecurity(_ context.Context, recs []secnorm.Record) {
	o.recs = append(o.recs, recs...)
}

func (o *recordingObserver) ObserveMetrics(context.Context, []hostmetrics.Sample, []hostmetrics.HostInfo) {
}

type env struct {
	t    *testing.T
	fake *fakeCF
	srv  *httptest.Server
	db   *telemetrydb.DB
	ing  *telemetry.Ingester
	obs  *recordingObserver
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := telemetrydb.Open(context.Background(), telemetrydb.Security, filepath.Join(t.TempDir(), "security.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fake := &fakeCF{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	ing := telemetry.New(db, nil, quiet())
	obs := &recordingObserver{}
	ing.SetObserver(obs)
	return &env{t: t, fake: fake, srv: srv, db: db, ing: ing, obs: obs}
}

func (e *env) poller(pageSize int, at time.Time) *Poller {
	return New(Config{
		Token: token, Zones: []string{zone}, Interval: time.Minute,
		Endpoint: e.srv.URL, PageSize: pageSize, Now: func() time.Time { return at },
	}, e.ing, quiet())
}

// rays returns the rayName of every stored row and fails on duplicates.
func (e *env) rays() map[string]bool {
	e.t.Helper()
	rows, err := e.db.SearchSecurity(context.Background(), telemetrydb.SecurityQuery{
		From: now.Add(-30 * 24 * time.Hour), To: now.Add(time.Hour), Limit: 500,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]bool{}
	for _, r := range rows {
		var raw map[string]any
		if err := json.Unmarshal([]byte(r.Raw), &raw); err != nil {
			e.t.Fatal(err)
		}
		ray := raw["rayName"].(string)
		if out[ray] {
			e.t.Fatalf("ray %s stored twice", ray)
		}
		out[ray] = true
		if r.Source != "cloudflare" || r.Kind != "waf" || r.SrcIP != "203.0.113.7" || r.Host != "wiebe.xyz" {
			e.t.Fatalf("row not normalized: %+v", r.Record)
		}
	}
	return out
}

func (e *env) cursor() cursor {
	e.t.Helper()
	v, err := e.ing.Cursor(context.Background(), CursorKey(zone))
	if err != nil {
		e.t.Fatal(err)
	}
	c, _ := parseCursor(v)
	return c
}

func mustPoll(t *testing.T, p *Poller) {
	t.Helper()
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRowsLandOnceAndReachObserver(t *testing.T) {
	e := newEnv(t)
	for i := range 5 {
		e.fake.add(fmt.Sprintf("ray%d", i), now.Add(-time.Hour+time.Duration(i)*time.Minute))
	}
	e.fake.add("too-recent", now.Add(-time.Minute)) // inside the 2m lag
	p := e.poller(100, now)
	mustPoll(t, p)
	mustPoll(t, p)
	if got := e.rays(); len(got) != 5 || got["too-recent"] {
		t.Fatalf("stored %v", got)
	}
	if len(e.obs.recs) != 5 || e.obs.recs[0].Source != "cloudflare" {
		t.Fatalf("observer saw %d records", len(e.obs.recs))
	}
	if c := e.cursor(); !c.TS.Equal(now.Add(-Lag)) {
		t.Fatalf("cursor = %v, want %v", c.TS, now.Add(-Lag))
	}
	if first := e.fake.requests[0]["datetime_geq"]; first != now.Add(-MaxLookback).Format(time.RFC3339) {
		t.Fatalf("first start since = %s", first)
	}
}

func TestCursorSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	base := now.Add(-30 * time.Minute)
	e.fake.add("a", base)
	e.fake.add("b", base.Add(time.Second))
	e.fake.add("c", base.Add(time.Second))
	mustPoll(t, e.poller(100, now))

	// More events arrive, one in the second the cursor ended on; a new
	// poller over the same file picks up where the old one stopped.
	later := now.Add(10 * time.Minute)
	e.fake.add("d", later.Add(-5*time.Minute))
	e.fake.add("e", now.Add(-Lag))
	mustPoll(t, e.poller(100, later))
	mustPoll(t, e.poller(100, later))
	if got := e.rays(); len(got) != 5 {
		t.Fatalf("stored %v", got)
	}
	if len(e.obs.recs) != 5 {
		t.Fatalf("observer saw %d records, want 5", len(e.obs.recs))
	}
}

func TestBoundaryTimestampsAcrossPages(t *testing.T) {
	e := newEnv(t)
	ts := now.Add(-10 * time.Minute)
	e.fake.add("x0", ts.Add(-time.Second))
	for i := range 5 {
		e.fake.add(fmt.Sprintf("same%d", i), ts)
	}
	e.fake.add("after", ts.Add(time.Second))
	p := e.poller(6, now)
	mustPoll(t, p)
	// Page size 6: x0 + five at ts fills the page exactly; the next page
	// re-reads ts and must store only "after".
	if got := e.rays(); len(got) != 7 {
		t.Fatalf("stored %d rows: %v", len(got), got)
	}
}

func TestIdenticalEventsInOneSecondStayDistinct(t *testing.T) {
	e := newEnv(t)
	ts := now.Add(-10 * time.Minute)
	e.fake.add("dup", ts)
	e.fake.add("dup", ts)
	e.fake.add("other", ts)
	p := e.poller(2, now)
	mustPoll(t, p)
	rows, err := e.db.SearchSecurity(context.Background(), telemetrydb.SecurityQuery{From: ts.Add(-time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("stored %d rows, want 3", len(rows))
	}
}

func TestCrowdedSecondIsDrainedWithWideQuery(t *testing.T) {
	e := newEnv(t)
	ts := now.Add(-10 * time.Minute)
	for i := range 5 {
		e.fake.add(fmt.Sprintf("s%d", i), ts)
	}
	e.fake.add("next", ts.Add(time.Second))
	mustPoll(t, e.poller(2, now))
	got := e.rays()
	if len(got) != 6 || !got["next"] {
		t.Fatalf("stored %v", got)
	}
	if len(e.fake.requests) > 5 {
		t.Fatalf("%d requests: the poller did not move past the crowded second", len(e.fake.requests))
	}
}

func TestOverfullSecondSkipsAheadInsteadOfLooping(t *testing.T) {
	e := newEnv(t)
	ts := now.Add(-10 * time.Minute)
	for i := range 5 {
		e.fake.add(fmt.Sprintf("s%d", i), ts)
	}
	e.fake.add("next", ts.Add(time.Second))
	p := e.poller(2, now)
	p.secondLimit = 3 // the wide query fills too: two events are lost
	mustPoll(t, p)
	got := e.rays()
	if len(got) != 4 || !got["next"] {
		t.Fatalf("stored %v", got)
	}
}

func TestOutageClampsToLookback(t *testing.T) {
	e := newEnv(t)
	stale := cursor{TS: now.Add(-72 * time.Hour)}
	if err := e.ing.IngestRecords(context.Background(), nil, CursorKey(zone), stale.encode()); err != nil {
		t.Fatal(err)
	}
	e.fake.add("old", now.Add(-48*time.Hour))
	e.fake.add("recent", now.Add(-time.Hour))
	mustPoll(t, e.poller(100, now))
	if got := e.rays(); len(got) != 1 || !got["recent"] {
		t.Fatalf("stored %v", got)
	}
	if since := e.fake.requests[0]["datetime_geq"]; since != now.Add(-MaxLookback).Format(time.RFC3339) {
		t.Fatalf("since = %s", since)
	}
}

func TestRateLimitBacksOff(t *testing.T) {
	e := newEnv(t)
	e.fake.add("a", now.Add(-time.Hour))
	e.fake.status = http.StatusTooManyRequests
	p := e.poller(100, now)
	if err := p.Poll(context.Background()); err == nil {
		t.Fatal("expected an error on 429")
	}
	if d := p.delay(); d < 2*time.Minute {
		t.Fatalf("delay after 429 = %s, want >= Retry-After 2m", d)
	}
	if len(e.rays()) != 0 {
		t.Fatal("rows stored on 429")
	}
	if c := e.cursor(); !c.TS.IsZero() {
		t.Fatalf("cursor moved on 429: %+v", c)
	}

	e.fake.status = 0
	e.fake.gqlError = "rate limiter budget depleted"
	if err := p.Poll(context.Background()); err == nil {
		t.Fatal("expected an error on a GraphQL error")
	}
	if d := p.delay(); d != 4*time.Minute {
		t.Fatalf("delay after two failures = %s, want 4m", d)
	}

	e.fake.gqlError = ""
	mustPoll(t, p)
	if d := p.delay(); d != time.Minute {
		t.Fatalf("delay after success = %s, want the interval", d)
	}
	if got := e.rays(); len(got) != 1 {
		t.Fatalf("stored %v after recovery", got)
	}
}

func TestBackoffCaps(t *testing.T) {
	p := New(Config{Interval: time.Minute}, nil, quiet())
	p.failures = 20
	if d := p.delay(); d != maxBackoff {
		t.Fatalf("delay = %s, want %s", d, maxBackoff)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.poller(100, now).Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
