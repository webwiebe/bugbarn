package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

type telemetryReadFixture struct {
	server  *Server
	session *http.Cookie
	ingest  string
	sec     *telemetrydb.DB
	met     *telemetrydb.DB
	now     time.Time
}

func newTelemetryReadFixture(t *testing.T) *telemetryReadFixture {
	t.Helper()
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })
	def, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, userAuth,
		auth.NewSessionManager("secret", time.Hour), nil, nil)

	dir := t.TempDir()
	sec, err := telemetrydb.Open(ctx, telemetrydb.Security, filepath.Join(dir, "security.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	met, err := telemetrydb.Open(ctx, telemetrydb.Metrics, filepath.Join(dir, "metrics.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sec.Close(); _ = met.Close() })
	server.SetTelemetryViews(telemetrydb.Fixed(sec), telemetrydb.Fixed(met))

	f := &telemetryReadFixture{
		server: server,
		ingest: createKey(t, store, def.ID, storage.APIKeyScopeIngest),
		sec:    sec, met: met,
		now: time.Now().UTC().Truncate(time.Minute),
	}
	f.session = f.login(t)
	return f
}

func (f *telemetryReadFixture) login(t *testing.T) *http.Cookie {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"pass"}`))
	f.server.ServeHTTP(rr, req)
	for _, c := range rr.Result().Cookies() {
		if c.Name == "bugbarn_session" {
			return c
		}
	}
	t.Fatalf("login failed: %d %s", rr.Code, rr.Body.String())
	return nil
}

func (f *telemetryReadFixture) get(t *testing.T, path string, out any) int {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(f.session)
	f.server.ServeHTTP(rr, req)
	if out != nil && rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, rr.Body.String())
		}
	}
	return rr.Code
}

type securityPage struct {
	Rows []struct {
		ID     int64  `json:"id"`
		SrcIP  string `json:"src_ip"`
		User   string `json:"user"`
		Status int    `json:"status"`
		Raw    string `json:"raw"`
	} `json:"rows"`
	NextCursor *string `json:"next_cursor"`
}

func TestTelemetrySecuritySearch(t *testing.T) {
	t.Parallel()
	f := newTelemetryReadFixture(t)
	var recs []secnorm.Record
	for i := 0; i < 5; i++ {
		recs = append(recs, secnorm.Record{
			TS: f.now.Add(-time.Duration(i) * time.Minute), Source: "sshd", Host: "node1", Kind: "auth_failure",
			SrcIP: "203.0.113.7", User: "root", Message: fmt.Sprintf("Failed password #%d", i), Raw: `{"n":1}`,
		})
	}
	recs = append(recs, secnorm.Record{TS: f.now, Source: "traefik", Host: "node2", Kind: "http",
		SrcIP: "198.51.100.1", Status: 403, Path: "/admin"})
	if err := f.sec.InsertSecurity(context.Background(), recs, f.now); err != nil {
		t.Fatal(err)
	}

	var page securityPage
	if code := f.get(t, "/api/v1/telemetry/security?src_ip=203.0.113.7&limit=3", &page); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(page.Rows) != 3 || page.NextCursor == nil || page.Rows[0].User != "root" || page.Rows[0].Raw != `{"n":1}` {
		t.Fatalf("first page = %+v", page)
	}
	var rest securityPage
	f.get(t, "/api/v1/telemetry/security?src_ip=203.0.113.7&limit=3&cursor="+*page.NextCursor, &rest)
	if len(rest.Rows) != 2 || rest.NextCursor != nil || rest.Rows[0].ID >= page.Rows[2].ID {
		t.Fatalf("second page = %+v", rest)
	}

	var byStatus securityPage
	f.get(t, "/api/v1/telemetry/security?status=403&q=admin", &byStatus)
	if len(byStatus.Rows) != 1 || byStatus.Rows[0].SrcIP != "198.51.100.1" {
		t.Fatalf("status/text filter = %+v", byStatus)
	}

	var old securityPage
	from := url.QueryEscape(f.now.Add(-48 * time.Hour).Format(time.RFC3339))
	to := url.QueryEscape(f.now.Add(-24 * time.Hour).Format(time.RFC3339))
	f.get(t, "/api/v1/telemetry/security?from="+from+"&to="+to, &old)
	if len(old.Rows) != 0 {
		t.Fatalf("time range filter returned %d rows", len(old.Rows))
	}

	for _, bad := range []string{"limit=9999", "limit=x", "status=7", "cursor=-1", "from=yesterday",
		"from=2026-10-04T12:00:00Z&to=2026-10-03T12:00:00Z"} {
		if code := f.get(t, "/api/v1/telemetry/security?"+bad, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", bad, code)
		}
	}
}

func TestTelemetryHostsAndSeries(t *testing.T) {
	t.Parallel()
	f := newTelemetryReadFixture(t)
	ctx := context.Background()
	samples := []hostmetrics.Sample{
		{Host: "k3s1", Metric: "load1", TS: f.now.Add(-2 * time.Minute), Value: 0.5},
		{Host: "k3s1", Metric: "load1", TS: f.now.Add(-time.Minute), Value: 0.7},
		{Host: "k3s1", Metric: "fs./.used_pct", TS: f.now.Add(-time.Minute), Value: 41},
	}
	hosts := []hostmetrics.HostInfo{{Host: "k3s1", Cores: 8, LastSeen: f.now}}
	if err := f.met.InsertMetrics(ctx, samples, hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := f.met.Write().ExecContext(ctx, `INSERT INTO samples_1h (host, metric, ts, min, avg, max, n)
		VALUES ('k3s1', 'load1', ?, 0.1, 0.4, 0.9, 60)`, f.now.Add(-10*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	var hostList struct {
		Hosts []telemetrydb.Host `json:"hosts"`
	}
	if code := f.get(t, "/api/v1/telemetry/hosts", &hostList); code != http.StatusOK {
		t.Fatalf("hosts status %d", code)
	}
	if len(hostList.Hosts) != 1 || hostList.Hosts[0].Cores != 8 || !hostList.Hosts[0].LastSeen.Equal(f.now) {
		t.Fatalf("hosts = %+v", hostList.Hosts)
	}

	var metrics struct {
		Metrics []string `json:"metrics"`
	}
	f.get(t, "/api/v1/telemetry/hosts/k3s1/metrics", &metrics)
	if strings.Join(metrics.Metrics, ",") != "fs./.used_pct,load1" {
		t.Fatalf("metrics = %v", metrics.Metrics)
	}

	type seriesResp struct {
		Resolution string              `json:"resolution"`
		Points     []telemetrydb.Point `json:"points"`
	}
	var minute seriesResp
	f.get(t, "/api/v1/telemetry/hosts/k3s1/series?metric=load1", &minute)
	if minute.Resolution != "1m" || len(minute.Points) != 2 || minute.Points[1].Value != 0.7 {
		t.Fatalf("minute series = %+v", minute)
	}
	var hourly seriesResp
	from := f.now.Add(-30 * 24 * time.Hour).UnixMilli()
	f.get(t, fmt.Sprintf("/api/v1/telemetry/hosts/k3s1/series?metric=load1&from=%d", from), &hourly)
	if hourly.Resolution != "1h" || len(hourly.Points) != 1 || hourly.Points[0].Max != 0.9 {
		t.Fatalf("hourly series = %+v", hourly)
	}
	// Mount points contain slashes; the client escapes them in the query.
	var fs seriesResp
	f.get(t, "/api/v1/telemetry/hosts/k3s1/series?metric="+url.QueryEscape("fs./.used_pct"), &fs)
	if len(fs.Points) != 1 {
		t.Fatalf("fs series = %+v", fs)
	}

	for path, want := range map[string]int{
		"/api/v1/telemetry/hosts/k3s1/series": http.StatusBadRequest,
		"/api/v1/telemetry/hosts/k3s1/series?metric=load1&from=1&to=" + fmt.Sprint(f.now.UnixMilli()): http.StatusBadRequest,
		"/api/v1/telemetry/hosts/k3s1/other":         http.StatusNotFound,
		"/api/v1/telemetry/hosts/k3s1/metrics/extra": http.StatusNotFound,
		"/api/v1/telemetry/hosts//metrics":           http.StatusNotFound,
	} {
		if code := f.get(t, path, nil); code != want {
			t.Errorf("%s: status %d, want %d", path, code, want)
		}
	}
}

func TestTelemetryReadAuth(t *testing.T) {
	t.Parallel()
	f := newTelemetryReadFixture(t)
	for name, setup := range map[string]func(*http.Request){
		"no credentials": func(*http.Request) {},
		"ingest key":     func(r *http.Request) { r.Header.Set("x-bugbarn-api-key", f.ingest) },
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, telemetryHostsPath, nil)
		setup(req)
		f.server.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 401 or 403", name, rr.Code)
		}
	}
}

// A reader pod opens the files lazily: 503 with a clear message until the
// writer has created them, then normal answers.
func TestTelemetryReadLazyReader(t *testing.T) {
	t.Parallel()
	f := newTelemetryReadFixture(t)
	dir := t.TempDir()
	sec := telemetrydb.NewLazy(telemetrydb.Security, filepath.Join(dir, "security.db"))
	met := telemetrydb.NewLazy(telemetrydb.Metrics, filepath.Join(dir, "metrics.db"))
	t.Cleanup(func() { _ = sec.Close(); _ = met.Close() })
	f.server.SetTelemetryViews(sec, met)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/security", nil)
	req.AddCookie(f.session)
	f.server.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "not been created yet") {
		t.Fatalf("before create: %d %q", rr.Code, rr.Body.String())
	}

	w, err := telemetrydb.Open(context.Background(), telemetrydb.Metrics, filepath.Join(dir, "metrics.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	var hosts struct {
		Hosts []telemetrydb.Host `json:"hosts"`
	}
	if code := f.get(t, telemetryHostsPath, &hosts); code != http.StatusOK || hosts.Hosts == nil {
		t.Fatalf("after create: status %d hosts %v", code, hosts.Hosts)
	}
	if code := f.get(t, telemetrySecurityPath, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("security still missing: status %d, want 503", code)
	}

	f.server.telemetryView = nil
	if code := f.get(t, telemetryHostsPath, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured: status %d, want 503", code)
	}
}
