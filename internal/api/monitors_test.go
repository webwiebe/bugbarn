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
