package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type monitorHarness struct {
	t      *testing.T
	server *Server
	store  *storage.Store
	key    string
	other  string
}

func newMonitorHarness(t *testing.T) *monitorHarness {
	t.Helper()
	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	geo, err := store.CreateProject(ctx, "geo", "geo")
	if err != nil {
		t.Fatal(err)
	}
	mint := func(name, plaintext string, projectID int64) {
		sum := sha256.Sum256([]byte(plaintext))
		if _, err := store.CreateAPIKey(ctx, name, projectID, hex.EncodeToString(sum[:]), storage.APIKeyScopeIngest); err != nil {
			t.Fatal(err)
		}
	}
	mint("geo-ingest", "geo-key", geo.ID)

	authorizer := auth.New("unbound-key").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, userAuth,
		auth.NewSessionManager("secret", time.Hour), nil, nil)
	return &monitorHarness{t: t, server: server, store: store, key: "geo-key", other: "unbound-key"}
}

func (h *monitorHarness) post(slug, key, body string, headers ...string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/"+slug+"/check-ins", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-BugBarn-API-Key", key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	h.server.ServeHTTP(rr, req)
	return rr
}

func TestMonitorCheckInEndpoint(t *testing.T) {
	t.Parallel()
	h := newMonitorHarness(t)

	t.Run("creates with an interval and answers 201", func(t *testing.T) {
		rr := h.post("pending-sweep", h.key, `{"status":"ok","interval":"1h","grace":"10m"}`)
		if rr.Code != http.StatusCreated {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out["monitor"] != "pending-sweep" || out["state"] != "ok" || out["interval"] != "1h0m0s" {
			t.Fatalf("body: %v", out)
		}
		if _, err := time.Parse(time.RFC3339, out["dueBy"].(string)); err != nil {
			t.Fatalf("dueBy: %v", err)
		}
	})

	t.Run("later check-in without timings answers 200", func(t *testing.T) {
		if rr := h.post("pending-sweep", h.key, `{"status":"ok"}`); rr.Code != http.StatusOK {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("an error check-in sets the state", func(t *testing.T) {
		rr := h.post("pending-sweep", h.key, `{"status":"error","message":"disk full"}`)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"state":"error"`) {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("unknown monitor without an interval is 404", func(t *testing.T) {
		if rr := h.post("ghost", h.key, `{"status":"ok"}`); rr.Code != http.StatusNotFound {
			t.Fatalf("got %d", rr.Code)
		}
	})

	t.Run("bad input is 400", func(t *testing.T) {
		for name, body := range map[string]string{
			"status":        `{"status":"maybe","interval":"1h"}`,
			"interval":      `{"status":"ok","interval":"10s"}`,
			"duration":      `{"status":"ok","interval":"soon"}`,
			"grace":         `{"status":"ok","interval":"1h","grace":"2h"}`,
			"not json":      `{`,
			"missing state": `{"interval":"1h"}`,
		} {
			if rr := h.post("bad", h.key, body); rr.Code != http.StatusBadRequest {
				t.Errorf("%s: got %d: %s", name, rr.Code, rr.Body.String())
			}
		}
		if rr := h.post("BadSlug", h.key, `{"status":"ok","interval":"1h"}`); rr.Code != http.StatusBadRequest {
			t.Errorf("slug: got %d", rr.Code)
		}
	})

	t.Run("no key and a wrong key are 401", func(t *testing.T) {
		for _, key := range []string{"", "nope"} {
			if rr := h.post("pending-sweep", key, `{"status":"ok"}`); rr.Code != http.StatusUnauthorized {
				t.Errorf("key %q: got %d", key, rr.Code)
			}
		}
	})

	t.Run("an unbound key needs a project header", func(t *testing.T) {
		body := `{"status":"ok","interval":"1h"}`
		if rr := h.post("job", h.other, body); rr.Code != http.StatusBadRequest {
			t.Errorf("no header: got %d", rr.Code)
		}
		if rr := h.post("job", h.other, body, "X-BugBarn-Project", "nowhere"); rr.Code != http.StatusNotFound {
			t.Errorf("unknown project: got %d", rr.Code)
		}
		if rr := h.post("job", h.other, body, "X-BugBarn-Project", "geo"); rr.Code != http.StatusCreated {
			t.Errorf("known project: got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("only POST is served", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors/pending-sweep/check-ins", nil)
		req.Header.Set("X-BugBarn-API-Key", h.key)
		rr := httptest.NewRecorder()
		h.server.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("got %d", rr.Code)
		}
	})
}

func TestMonitorCheckInSlugPath(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		"/api/v1/monitors/a/check-ins":   "a",
		"/api/v1/monitors//check-ins":    "",
		"/api/v1/monitors/a/b/check-ins": "",
		"/api/v1/monitors/a":             "",
		"/api/v1/issues":                 "",
	} {
		got, ok := monitorCheckInSlug(path)
		if (want == "") == ok || got != want {
			t.Errorf("%s: got %q ok=%v, want %q", path, got, ok, want)
		}
	}
}

func TestMonitorCheckInIsForwardedByReaders(t *testing.T) {
	t.Parallel()
	h := newMonitorHarness(t)
	var gotPath, gotKey string
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("X-BugBarn-API-Key")
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(writer.Close)
	h.server.SetWriteForwarder(NewWriteForwarder(writer.URL))

	if rr := h.post("job", "", `{"status":"ok","interval":"1h"}`); rr.Code != http.StatusUnauthorized || gotPath != "" {
		t.Fatalf("a keyless check-in reached the writer: %d %q", rr.Code, gotPath)
	}
	if rr := h.post("job", h.key, `{"status":"ok","interval":"1h"}`); rr.Code != http.StatusCreated {
		t.Fatalf("forwarded check-in: %d", rr.Code)
	}
	if gotPath != "/api/v1/monitors/job/check-ins" || gotKey != h.key {
		t.Fatalf("writer saw %q with key %q", gotPath, gotKey)
	}
	if list, _ := h.store.ListMonitors(context.Background(), 1); len(list) != 0 {
		t.Fatal("the reader wrote the monitor itself")
	}
}

func TestListMonitorsPutsIncidentsFirst(t *testing.T) {
	t.Parallel()
	h := newMonitorHarness(t)
	if rr := h.post("a-sweep", h.key, `{"status":"ok","interval":"1h"}`); rr.Code != http.StatusCreated {
		t.Fatalf("create ok monitor: %d %s", rr.Code, rr.Body)
	}
	if rr := h.post("b-sync", h.key, `{"status":"error","interval":"1h"}`); rr.Code != http.StatusCreated {
		t.Fatalf("create failing monitor: %d %s", rr.Code, rr.Body)
	}

	get := func(headers ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rr := httptest.NewRecorder()
		h.server.ServeHTTP(rr, req)
		return rr
	}
	if rr := get(); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", rr.Code)
	}
	rr := get("X-BugBarn-API-Key", h.other, "X-BugBarn-Project", "geo")
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	var body struct {
		Monitors []map[string]any `json:"monitors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Monitors) != 2 || body.Monitors[0]["slug"] != "b-sync" || body.Monitors[0]["state"] != "error" || body.Monitors[1]["slug"] != "a-sweep" {
		t.Fatalf("order or content: %v", body.Monitors)
	}
	if body.Monitors[1]["intervalSeconds"] != float64(3600) || body.Monitors[1]["lastCheckInAt"] == nil {
		t.Fatalf("fields: %v", body.Monitors[1])
	}

}

func TestDeleteMonitorEndpoint(t *testing.T) {
	t.Parallel()
	h := newMonitorHarness(t)
	if rr := h.post("sweep", h.key, `{"status":"ok","interval":"1h"}`); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body)
	}
	del := func(path string, headers ...string) int {
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rr := httptest.NewRecorder()
		h.server.ServeHTTP(rr, req)
		return rr.Code
	}
	scope := []string{"X-BugBarn-API-Key", h.other, "X-BugBarn-Project", "geo"}
	if code := del("/api/v1/monitors/sweep"); code != http.StatusUnauthorized {
		t.Fatalf("anonymous delete: %d", code)
	}
	if code := del("/api/v1/monitors/nope", scope...); code != http.StatusNotFound {
		t.Fatalf("unknown monitor: %d", code)
	}
	if code := del("/api/v1/monitors/sweep", scope...); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code := del("/api/v1/monitors/sweep", scope...); code != http.StatusNotFound {
		t.Fatalf("second delete: %d", code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("X-BugBarn-API-Key", h.other)
	req.Header.Set("X-BugBarn-Project", "geo")
	rr := httptest.NewRecorder()
	h.server.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `"monitors":[]`) {
		t.Fatalf("list after delete: %s", rr.Body)
	}
}

func TestMonitorCheckInWithSchedule(t *testing.T) {
	t.Parallel()
	h := newMonitorHarness(t)

	rr := h.post("nightly", h.key, `{"status":"ok","schedule":"0 6 * * *","timezone":"Europe/Amsterdam","grace":"10m"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["schedule"] != "0 6 * * *" || created["timezone"] != "Europe/Amsterdam" {
		t.Fatalf("response: %v", created)
	}

	// No timezone means UTC; the listing carries the schedule and no interval.
	if rr := h.post("utc-job", h.key, `{"status":"ok","schedule":"*/15 * * * *"}`); rr.Code != http.StatusCreated {
		t.Fatalf("utc: got %d: %s", rr.Code, rr.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	req.Header.Set("X-BugBarn-API-Key", h.other)
	req.Header.Set("X-BugBarn-Project", "geo")
	list := httptest.NewRecorder()
	h.server.ServeHTTP(list, req)
	var body struct {
		Monitors []map[string]any `json:"monitors"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, list.Body.String())
	}
	got := map[string]map[string]any{}
	for _, m := range body.Monitors {
		got[m["slug"].(string)] = m
	}
	if got["utc-job"]["timezone"] != "UTC" || got["utc-job"]["schedule"] != "*/15 * * * *" || got["utc-job"]["intervalSeconds"] != float64(0) {
		t.Fatalf("listing: %v", got["utc-job"])
	}
	if got["nightly"]["graceSeconds"] != float64(600) {
		t.Fatalf("listing: %v", got["nightly"])
	}

	for name, payload := range map[string]string{
		"both modes":       `{"status":"ok","interval":"1h","schedule":"0 6 * * *"}`,
		"bad expression":   `{"status":"ok","schedule":"every day"}`,
		"six fields":       `{"status":"ok","schedule":"0 0 6 * * *"}`,
		"never runs":       `{"status":"ok","schedule":"0 0 31 2 *"}`,
		"unknown zone":     `{"status":"ok","schedule":"0 6 * * *","timezone":"Mars/Olympus"}`,
		"zone on interval": `{"status":"ok","interval":"1h","timezone":"Europe/Amsterdam"}`,
	} {
		if rr := h.post("bad-cron", h.key, payload); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d: %s", name, rr.Code, rr.Body.String())
		}
	}
}
