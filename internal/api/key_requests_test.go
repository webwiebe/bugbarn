package api

import (
	"context"
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

type keyRequestHarness struct {
	t        *testing.T
	server   *Server
	sessions *auth.SessionManager
	store    *storage.Store
	session  *http.Cookie
}

func newKeyRequestHarness(t *testing.T) *keyRequestHarness {
	t.Helper()
	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })
	if _, err := store.CreateProject(context.Background(), "geo", "geo"); err != nil {
		t.Fatal(err)
	}
	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager("test-secret", time.Hour)
	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, userAuth, sessions, nil, nil)
	server.SetSetupConfig("secret", "https://bugs.example.com")

	h := &keyRequestHarness{t: t, server: server, sessions: sessions, store: store}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"change-me"}`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)
	for _, c := range rr.Result().Cookies() {
		if c.Name == "bugbarn_session" {
			h.session = c
		}
	}
	if h.session == nil {
		t.Fatalf("no session cookie: %s", rr.Body.String())
	}
	return h
}

func (h *keyRequestHarness) do(method, path, body, pollSecret string, admin bool) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if pollSecret != "" {
		req.Header.Set(pollSecretHeader, pollSecret)
	}
	if admin {
		req.AddCookie(h.session)
		req.Header.Set("X-BugBarn-CSRF", h.sessions.CSRFToken(h.session.Value))
	}
	rr := httptest.NewRecorder()
	h.server.ServeHTTP(rr, req)
	return rr
}

type createdRequest struct {
	ID         string `json:"id"`
	PollSecret string `json:"pollSecret"`
}

func (h *keyRequestHarness) create(label string) createdRequest {
	h.t.Helper()
	rr := h.do(http.MethodPost, "/api/v1/setup/geo/key-requests", `{"label":"`+label+`"}`, "", false)
	if rr.Code != http.StatusAccepted {
		h.t.Fatalf("create: got %d body=%s", rr.Code, rr.Body.String())
	}
	var c createdRequest
	decodeResponse(h.t, rr, &c)
	if c.ID == "" || c.PollSecret == "" {
		h.t.Fatalf("create returned no id or secret: %s", rr.Body.String())
	}
	return c
}

func (h *keyRequestHarness) poll(c createdRequest) (int, map[string]any) {
	h.t.Helper()
	rr := h.do(http.MethodGet, "/api/v1/setup/geo/key-requests/"+c.ID, "", c.PollSecret, false)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func TestKeyRequestApprovedKeyIsClaimedOnce(t *testing.T) {
	t.Parallel()
	h := newKeyRequestHarness(t)
	c := h.create("webwiebe/geo production")

	if code, out := h.poll(c); code != http.StatusOK || out["status"] != "pending" || out["apiKey"] != nil {
		t.Fatalf("pending poll: %d %v", code, out)
	}

	list := h.do(http.MethodGet, "/api/v1/key-requests", "", "", true)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "webwiebe/geo production") {
		t.Fatalf("admin list: %d %s", list.Code, list.Body.String())
	}
	if rr := h.do(http.MethodPost, "/api/v1/key-requests/"+c.ID+"/approve", "{}", "", true); rr.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}

	code, out := h.poll(c)
	key, _ := out["apiKey"].(string)
	if code != http.StatusOK || out["status"] != "approved" || key == "" {
		t.Fatalf("claim poll: %d %v", code, out)
	}
	code, out = h.poll(c)
	if code != http.StatusOK || out["status"] != "claimed" || out["apiKey"] != nil {
		t.Fatalf("second poll must not return the key: %d %v", code, out)
	}

	// The minted key is a working project-scoped ingest key.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(`{"name":"v1","environment":"production","version":"v1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BugBarn-Api-Key", key)
	req.Header.Set("X-BugBarn-Project", "geo")
	rr := httptest.NewRecorder()
	h.server.ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
		t.Fatalf("minted key rejected: %d %s", rr.Code, rr.Body.String())
	}
	// It is an ingest-scope key: other endpoints stay closed to it.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)
	req.Header.Set("X-BugBarn-Api-Key", key)
	rr = httptest.NewRecorder()
	h.server.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("minted key reached /issues: %d", rr.Code)
	}
}

func TestKeyRequestAnonymousNeverGetsKeyWithoutApproval(t *testing.T) {
	t.Parallel()
	h := newKeyRequestHarness(t)
	c := h.create("agent")

	if code, out := h.poll(c); out["apiKey"] != nil {
		t.Fatalf("pending request revealed a key: %d %v", code, out)
	}
	// The admin endpoints need a session.
	if rr := h.do(http.MethodPost, "/api/v1/key-requests/"+c.ID+"/approve", "{}", "", false); rr.Code == http.StatusOK {
		t.Fatalf("anonymous approve succeeded")
	}
	if rr := h.do(http.MethodGet, "/api/v1/key-requests", "", "", false); rr.Code == http.StatusOK {
		t.Fatalf("anonymous list succeeded")
	}
	if code, out := h.poll(c); out["apiKey"] != nil || out["status"] != "pending" {
		t.Fatalf("still pending expected: %d %v", code, out)
	}
	// A wrong or missing poll secret reveals nothing about the request.
	rr := h.do(http.MethodGet, "/api/v1/setup/geo/key-requests/"+c.ID, "", "wrong", false)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("wrong secret: got %d", rr.Code)
	}
	rr = h.do(http.MethodGet, "/api/v1/setup/geo/key-requests/"+c.ID, "", "", false)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing secret: got %d", rr.Code)
	}
}

func TestKeyRequestDeniedNeverRevealsKey(t *testing.T) {
	t.Parallel()
	h := newKeyRequestHarness(t)
	c := h.create("agent")
	if rr := h.do(http.MethodPost, "/api/v1/key-requests/"+c.ID+"/deny", "{}", "", true); rr.Code != http.StatusOK {
		t.Fatalf("deny: %d %s", rr.Code, rr.Body.String())
	}
	if code, out := h.poll(c); code != http.StatusOK || out["status"] != "denied" || out["apiKey"] != nil {
		t.Fatalf("denied poll: %d %v", code, out)
	}
	// A denied request cannot be approved afterwards.
	if rr := h.do(http.MethodPost, "/api/v1/key-requests/"+c.ID+"/approve", "{}", "", true); rr.Code != http.StatusConflict {
		t.Fatalf("approve after deny: got %d", rr.Code)
	}
	if code, out := h.poll(c); out["apiKey"] != nil {
		t.Fatalf("key leaked after deny: %d %v", code, out)
	}
}

func TestKeyRequestExpiredReturnsGone(t *testing.T) {
	t.Parallel()
	h := newKeyRequestHarness(t)
	c := h.create("agent")
	if rr := h.do(http.MethodPost, "/api/v1/key-requests/"+c.ID+"/approve", "{}", "", true); rr.Code != http.StatusOK {
		t.Fatalf("approve: %d", rr.Code)
	}
	h.server.keyRequests.SetClock(func() time.Time { return time.Now().Add(25 * time.Hour) })

	code, out := h.poll(c)
	if code != http.StatusGone || out["status"] != "expired" || out["apiKey"] != nil {
		t.Fatalf("expired poll: %d %v", code, out)
	}
}

func TestKeyRequestNeedsActiveProjectAndLabel(t *testing.T) {
	t.Parallel()
	h := newKeyRequestHarness(t)
	if rr := h.do(http.MethodPost, "/api/v1/setup/unknown-app/key-requests", `{"label":"x"}`, "", false); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project: got %d", rr.Code)
	}
	if rr := h.do(http.MethodPost, "/api/v1/setup/geo/key-requests", `{"label":"  "}`, "", false); rr.Code != http.StatusBadRequest {
		t.Fatalf("blank label: got %d", rr.Code)
	}
}

func TestKeyRequestPendingCap(t *testing.T) {
	t.Parallel()
	h := newKeyRequestHarness(t)
	for range 10 {
		h.create("agent")
	}
	rr := h.do(http.MethodPost, "/api/v1/setup/geo/key-requests", `{"label":"one too many"}`, "", false)
	if rr.Code != http.StatusConflict {
		t.Fatalf("11th request: got %d", rr.Code)
	}
}

// A reader never writes SQLite: both the create and the claiming poll go to the writer.
func TestKeyRequestEndpointsForwardOnReader(t *testing.T) {
	t.Parallel()
	var got []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+" secret="+r.Header.Get(pollSecretHeader))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/setup/geo/key-requests"},
		{http.MethodGet, "/api/v1/setup/geo/key-requests/abc"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"label":"x"}`))
		req.Header.Set(pollSecretHeader, "s3cret")
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("%s %s: got %d, want the writer's 202", tc.method, tc.path, rr.Code)
		}
	}
	want := []string{
		"POST /api/v1/setup/geo/key-requests secret=s3cret",
		"GET /api/v1/setup/geo/key-requests/abc secret=s3cret",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("forwarded %v, want %v", got, want)
	}
}
