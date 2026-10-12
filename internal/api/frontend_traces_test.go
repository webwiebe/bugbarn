package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

type upstreamCall struct {
	path   string
	apiKey string
	ctype  string
	body   string
}

// newFrontendTracesServer builds an auth-enabled server whose frontend-traces
// proxy points at a fake SpanBarn, and returns a logged-in session cookie.
func newFrontendTracesServer(t *testing.T, upstream http.HandlerFunc) (*Server, *http.Cookie) {
	t.Helper()

	spanbarn := httptest.NewServer(upstream)
	t.Cleanup(spanbarn.Close)

	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })

	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager("test-secret", time.Hour)
	server := NewServerWithAuth(nil, store, userAuth, sessions, nil, nil)
	server.SetFrontendTraces(spanbarn.URL, "server-held-key", "bugbarn-web")

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"change-me"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(loginRec, loginReq)
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == "bugbarn_session" {
			return server, c
		}
	}
	t.Fatalf("no session cookie, login body=%q", loginRec.Body.String())
	return nil, nil
}

func postSpans(server *Server, session *http.Cookie, body string, browserKey string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, frontendTracesPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if browserKey != "" {
		req.Header.Set("X-SpanBarn-Api-Key", browserKey)
	}
	if session != nil {
		req.AddCookie(session)
	}
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	return rr
}

// The browser SDK sends spans with a session cookie and no CSRF header. The
// proxy must forward them to SpanBarn under the server-held key and never pass
// on whatever key the browser claims to have.
func TestFrontendTracesForwardsWithServerKey(t *testing.T) {
	t.Parallel()

	calls := make(chan upstreamCall, 1)
	server, session := newFrontendTracesServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls <- upstreamCall{path: r.URL.Path, apiKey: r.Header.Get("X-SpanBarn-Api-Key"), ctype: r.Header.Get("Content-Type"), body: string(b)}
		w.WriteHeader(http.StatusAccepted)
	})

	body := `{"spans":[{"name":"page.load","service":"bugbarn-web"}]}`
	rr := postSpans(server, session, body, "browser-supplied-key")
	if rr.Code < 200 || rr.Code > 299 {
		t.Fatalf("status = %d body=%q; want 2xx", rr.Code, rr.Body.String())
	}

	got := <-calls
	if got.path != "/api/v1/spans" {
		t.Fatalf("upstream path = %q; want /api/v1/spans", got.path)
	}
	if got.apiKey != "server-held-key" {
		t.Fatalf("upstream key = %q; want the server-held key", got.apiKey)
	}
	if got.ctype != "application/json" {
		t.Fatalf("upstream content type = %q", got.ctype)
	}
	if got.body != body {
		t.Fatalf("upstream body = %q; want %q", got.body, body)
	}
}

func TestFrontendTracesRequiresLogin(t *testing.T) {
	t.Parallel()

	var hit atomic.Bool
	server, _ := newFrontendTracesServer(t, func(http.ResponseWriter, *http.Request) { hit.Store(true) })

	rr := postSpans(server, nil, `{"spans":[]}`, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rr.Code)
	}
	if hit.Load() {
		t.Fatal("an unauthenticated request reached SpanBarn")
	}
}

func TestFrontendTracesRejectsOversizeBody(t *testing.T) {
	t.Parallel()

	var hit atomic.Bool
	server, session := newFrontendTracesServer(t, func(http.ResponseWriter, *http.Request) { hit.Store(true) })

	rr := postSpans(server, session, `{"spans":"`+strings.Repeat("x", frontendTracesMaxBody)+`"}`, "")
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413", rr.Code)
	}
	if hit.Load() {
		t.Fatal("an oversize body reached SpanBarn")
	}
}

func TestFrontendTracesOnlyAcceptsPost(t *testing.T) {
	t.Parallel()

	server, session := newFrontendTracesServer(t, func(http.ResponseWriter, *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, frontendTracesPath, nil)
	req.AddCookie(session)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d; want 405", rr.Code)
	}
}

func TestFrontendTracesReportsUpstreamFailure(t *testing.T) {
	t.Parallel()

	server, session := newFrontendTracesServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	rr := postSpans(server, session, `{"spans":[]}`, "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", rr.Code)
	}
}

func TestFrontendTracesNotConfiguredIsNotFound(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	rr := postSpans(server, nil, `{"spans":[]}`, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rr.Code)
	}
}

// A reader forwards authenticated writes to the writer. The proxy keeps no
// state, so the reader serves it itself instead of loading the writer.
func TestFrontendTracesIsServedByTheReader(t *testing.T) {
	t.Parallel()

	var writerHit atomic.Bool
	writer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { writerHit.Store(true) }))
	t.Cleanup(writer.Close)

	calls := make(chan upstreamCall, 1)
	server, session := newFrontendTracesServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls <- upstreamCall{path: r.URL.Path}
		w.WriteHeader(http.StatusAccepted)
	})
	server.SetWriteForwarder(NewWriteForwarder(writer.URL))

	rr := postSpans(server, session, `{"spans":[]}`, "")
	if rr.Code < 200 || rr.Code > 299 {
		t.Fatalf("status = %d; want 2xx", rr.Code)
	}
	<-calls
	if writerHit.Load() {
		t.Fatal("the reader forwarded the frontend traces to the writer")
	}
}

// The runtime config tells the SPA whether to start the SDK and where to send
// spans. It must not carry the key.
func TestRuntimeConfigAdvertisesFrontendTraces(t *testing.T) {
	t.Parallel()

	server, _ := newFrontendTracesServer(t, func(http.ResponseWriter, *http.Request) {})

	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/runtime-config", nil))
	if strings.Contains(rr.Body.String(), "server-held-key") {
		t.Fatalf("runtime config leaks the key: %s", rr.Body.String())
	}
	var cfg struct {
		SpanBarn struct {
			Enabled  bool   `json:"enabled"`
			Endpoint string `json:"endpoint"`
			Service  string `json:"service"`
		} `json:"spanbarn"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.SpanBarn.Enabled || cfg.SpanBarn.Endpoint != "/api/v1/frontend-traces" || cfg.SpanBarn.Service != "bugbarn-web" {
		t.Fatalf("spanbarn block = %+v", cfg.SpanBarn)
	}
}
