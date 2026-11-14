package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// scopedReadRoute is a protected GET route that returns project data. mark is
// text only the seeded default project's row carries, so a body that holds it
// belongs to that project.
type scopedReadRoute struct {
	path string
	mark string
}

var scopedReadRoutes = []scopedReadRoute{
	{"/api/v1/logs", "homemark-log"},
	{"/api/v1/releases", "homemark-release"},
	{"/api/v1/alerts", "homemark-alert"},
	{"/api/v1/aliases", "homemark-alias"},
	{"/api/v1/surfaces", "homemark-surface"},
	{"/api/v1/facets/attributes.surface", "homemark-surface"},
	{"/api/v1/analytics/overview", `"pageviews":1`},
	{"/api/v1/source-maps", "homemark-bundle"},
	{"/api/v1/monitors", "homemark-monitor"},
	{"/api/v1/key-requests", "homemark-key-request"},
}

// routesCoveredElsewhere lists the protected GET patterns that no seeded-row
// case above covers, each with where it is covered or why it needs none. Keys
// are the registered paths, wildcards included.
var routesCoveredElsewhere = map[string]string{
	"/api/v1/issues":                         "TestBoundKeyIsConfinedToItsProject",
	"/api/v1/issues/sparklines":              "TestBoundKeyIsConfinedToItsProject",
	"/api/v1/issues/{id}":                    "TestBoundKeyIsConfinedToItsProject (inIssueScope)",
	"/api/v1/issues/{id}/events":             "TestBoundKeyIsConfinedToItsProject (inIssueScope)",
	"/api/v1/issues/{id}/releases":           "inIssueScope; releases of an issue of the request's project",
	"/api/v1/events/{id}":                    "TestBoundReadKeySeesOnlyItsProject",
	"/api/v1/projects":                       "TestBoundReadKeySeesOnlyItsProject",
	"/api/v1/projects/pending-count":         "TestBoundReadKeySeesOnlyItsProject",
	"/api/v1/projects/{slug}/silence-watch":  "one project's rule, read by slug",
	"/api/v1/groups":                         "TestBoundReadKeySeesOnlyItsProject",
	"/api/v1/groups/{slug}/projects":         "TestBoundReadKeySeesOnlyItsProject",
	"/api/v1/settings":                       "a read key is refused /settings",
	"/api/v1/apikeys":                        "a read key is refused /apikeys",
	"/api/v1/releases/{id}":                  "one release by id, filtered by project_id in the store",
	"/api/v1/alerts/{id}":                    "one alert by id, filtered by project_id in the store",
	"/api/v1/facets":                         "the facet keys of the request's project, like /api/v1/facets/{key}",
	"/api/v1/facets/{key}":                   "the facets/attributes.surface case above",
	"/api/v1/analytics/pages":                "same analyticsView project scope as /api/v1/analytics/overview",
	"/api/v1/analytics/timeline":             "same analyticsView project scope as /api/v1/analytics/overview",
	"/api/v1/analytics/referrers":            "same analyticsView project scope as /api/v1/analytics/overview",
	"/api/v1/analytics/segments":             "same analyticsView project scope as /api/v1/analytics/overview",
	"/api/v1/analytics/flow":                 "same analyticsView project scope as /api/v1/analytics/overview",
	"/api/v1/analytics/scroll":               "same analyticsView project scope as /api/v1/analytics/overview",
	"/api/v1/analytics/dropout":              "same analyticsView project scope as /api/v1/analytics/overview",
	"/api/v1/logs/stream":                    "SSE stream of the request's project",
	"/api/v1/logs/histogram":                 "TestLogExplorerEndpoints; same logQueryFromRequest project scope as /api/v1/logs",
	"/api/v1/logs/facets":                    "TestLogExplorerEndpoints; same logQueryFromRequest project scope as /api/v1/logs",
	"/api/v1/events/stream":                  "SSE stream of the request's project",
	"/api/v1/events/timeline":                "aggregate counts of the request's project",
	"/api/v1/live/events":                    "events of the request's project",
	"/api/v1/detection/rules":                "detection rules are global, not per project",
	"/api/v1/telemetry/security":             "infrastructure telemetry is global, not per project",
	"/api/v1/telemetry/hosts":                "infrastructure telemetry is global, not per project",
	"/api/v1/telemetry/hosts/{host}/metrics": "infrastructure telemetry is global, not per project",
	"/api/v1/telemetry/hosts/{host}/series":  "infrastructure telemetry is global, not per project",
	"/internal/v1/db-backup":                 "not reachable with an API key",
}

func seedScopedRows(t *testing.T, store *storage.Store, home domain.Project) {
	t.Helper()
	ctx := domain.WithProjectID(context.Background(), home.ID)
	now := time.Now().UTC()

	if err := store.InsertLogEntries(ctx, []storage.LogEntry{{
		ProjectID: home.ID, ReceivedAt: now, LevelNum: 30, Level: "info", Message: "homemark-log",
	}}); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	if _, err := store.CreateRelease(ctx, storage.Release{Name: "homemark-release", ObservedAt: now}); err != nil {
		t.Fatalf("seed release: %v", err)
	}
	if _, err := store.CreateAlert(ctx, storage.Alert{Name: "homemark-alert", Severity: "error", Enabled: true}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	if err := store.CreateAlias(ctx, "homemark-alias", home.ID); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	if _, err := store.UploadSourceMap(ctx, storage.SourceMapUpload{
		Release: "r1", BundleURL: "homemark-bundle.js", Blob: []byte("{}"),
	}); err != nil {
		t.Fatalf("seed source map: %v", err)
	}
	interval := time.Hour
	if _, err := store.RecordMonitorCheckIn(ctx, storage.MonitorCheckIn{
		ProjectID: home.ID, Slug: "homemark-monitor", Status: "ok", Interval: &interval, At: now,
	}); err != nil {
		t.Fatalf("seed monitor: %v", err)
	}
	if err := store.CreateKeyRequest(ctx, domain.KeyRequest{
		ID: "kr-homemark", ProjectID: home.ID, Label: "homemark-key-request",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, "poll"); err != nil {
		t.Fatalf("seed key request: %v", err)
	}
	if err := store.InsertPageView(ctx, analytics.PageView{
		ProjectID: home.ID, Ts: now, Pathname: "/", Hostname: "example.test", SessionID: "s1", VisitorID: "v1",
	}); err != nil {
		t.Fatalf("seed page view: %v", err)
	}
	pe := event.ProcessedEvent{
		Event: event.Event{
			ObservedAt: now, ReceivedAt: now.Add(time.Second), Severity: "ERROR", Message: "surface error",
			Exception:  event.Exception{Type: "TestError", Message: "surface error"},
			Attributes: map[string]any{"surface": "homemark-surface"},
		},
		Fingerprint:         "test-scoped-routes-fingerprint",
		FingerprintMaterial: "TestError: surface error",
	}
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, pe); err != nil {
		t.Fatalf("seed surface event: %v", err)
	}
}

// Every project-data GET route answers a bound read key from its own project
// only (#235). The default project holds one seeded row per route; a key bound
// to another project must not see it, and a full-scope key must, so an empty
// body cannot pass the test by accident.
func TestBoundReadKeyRoutesCarryNoOtherProjectData(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	ctx := context.Background()

	home, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := store.CreateProject(ctx, "Alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	seedScopedRows(t, store, home)

	addKey := func(name string, projectID int64, scope string) string {
		plain := name + "-secret"
		sum := sha256.Sum256([]byte(plain))
		if _, err := store.CreateAPIKey(ctx, name, projectID, hex.EncodeToString(sum[:]), scope); err != nil {
			t.Fatal(err)
		}
		return plain
	}
	alphaRead := addKey("alpha-read", alpha.ID, storage.APIKeyScopeRead)
	fullKey := addKey("home-full", home.ID, storage.APIKeyScopeFull)

	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	sessions := auth.NewSessionManager("secret", time.Hour)
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, userAuth, sessions, nil, nil)

	get := func(key, path string) (int, string) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req.Header.Set("x-bugbarn-api-key", key)
		server.ServeHTTP(rr, req)
		return rr.Code, strings.ReplaceAll(rr.Body.String(), " ", "")
	}

	for _, rt := range scopedReadRoutes {
		code, body := get(fullKey, rt.path)
		if code != http.StatusOK || !strings.Contains(body, rt.mark) {
			t.Errorf("%s: full-scope key = %d, want 200 with %q (seed missing?): %s", rt.path, code, rt.mark, body)
			continue
		}
		code, body = get(alphaRead, rt.path)
		if code != http.StatusOK {
			t.Errorf("%s: bound key = %d, want 200", rt.path, code)
		}
		if strings.Contains(body, rt.mark) {
			t.Errorf("%s: bound key to another project sees %q: %s", rt.path, rt.mark, body)
		}
	}
}

// A protected route added without a seeded-row case or a reason fails here, so a
// new route cannot reach other projects' data unnoticed. Only the operations
// registered by registerProtectedRoutes are checked.
func TestEveryProtectedRouteIsScopedOrExplained(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	rt := &router{mux: http.NewServeMux()}
	srv.registerProtectedRoutes(rt)
	if len(rt.operations) < 80 {
		t.Fatalf("found %d protected operations, the route table lost routes", len(rt.operations))
	}

	known := map[string]bool{}
	for _, sr := range scopedReadRoutes {
		known[normalizePath(sr.path)] = true
	}
	for path := range routesCoveredElsewhere {
		known[normalizePath(path)] = true
	}
	for _, pattern := range rt.operations {
		method, path, _ := strings.Cut(pattern, " ")
		if method != http.MethodGet {
			continue
		}
		if !known[normalizePath(path)] {
			t.Errorf("protected route %q has no entry in scopedReadRoutes or routesCoveredElsewhere", pattern)
		}
	}
}
