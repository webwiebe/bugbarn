package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteForwarder_IngestPOSTForwarded(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{"error":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%q", rr.Code, rr.Body.String())
	}
	if gotMethod != http.MethodPost {
		t.Errorf("upstream got method %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/events" {
		t.Errorf("upstream got path %s, want /api/v1/events", gotPath)
	}
	if string(gotBody) != `{"error":"test"}` {
		t.Errorf("upstream got body %q", gotBody)
	}
}

func TestWriteForwarder_IngestOPTIONSNotForwarded(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/events", nil)
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if upstreamCalled {
		t.Error("upstream should not be called for OPTIONS preflight")
	}
}

func TestWriteForwarder_LogPOSTForwarded(t *testing.T) {
	t.Parallel()

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", strings.NewReader(`[{"level":"error","message":"oops"}]`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%q", rr.Code, rr.Body.String())
	}
	if gotPath != "/api/v1/logs" {
		t.Errorf("upstream got path %s, want /api/v1/logs", gotPath)
	}
}

func TestWriteForwarder_AnalyticsCollectForwarded(t *testing.T) {
	t.Parallel()

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/collect", strings.NewReader(`{"url":"https://example.com","referrer":""}`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%q", rr.Code, rr.Body.String())
	}
	if gotPath != "/api/v1/analytics/collect" {
		t.Errorf("upstream got path %s, want /api/v1/analytics/collect", gotPath)
	}
}

func TestWriteForwarder_GETNotForwarded(t *testing.T) {
	t.Parallel()

	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	for _, path := range []string{"/api/v1/issues", "/api/v1/health", "/api/v1/projects"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		server.ServeHTTP(rr, req)

		if rr.Code == http.StatusBadGateway {
			t.Errorf("GET %s should not be forwarded, got 502", path)
		}
	}
	if upstreamCalled {
		t.Error("upstream should not be called for GET requests")
	}
}

func TestWriteForwarder_AuthenticatedPOSTForwarded(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store := mustOpenStore(t)
	defer store.Close()

	// No auth configured — acts like unauthenticated server (all protected routes open).
	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	rr := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"v1.0.0"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/releases", body)
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	if gotMethod != http.MethodPost {
		t.Errorf("upstream got method %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/releases" {
		t.Errorf("upstream got path %s, want /api/v1/releases", gotPath)
	}
}

func TestWriteForwarder_LoginForwarded(t *testing.T) {
	t.Parallel()

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Set-Cookie", "bugbarn_session=abc; Path=/")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"pass"}`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	if gotPath != "/api/v1/login" {
		t.Errorf("upstream got path %s, want /api/v1/login", gotPath)
	}
	if cookie := rr.Header().Get("Set-Cookie"); !strings.Contains(cookie, "bugbarn_session") {
		t.Errorf("expected session cookie from upstream, got %q", cookie)
	}
}

func TestWriteForwarder_DeleteForwarded(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(upstream.URL))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/test-proj", nil)
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%q", rr.Code, rr.Body.String())
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("upstream got method %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/v1/projects/test-proj" {
		t.Errorf("upstream got path %s, want /api/v1/projects/test-proj", gotPath)
	}
}

func TestWriteForwarder_WriterDown502(t *testing.T) {
	t.Parallel()

	// Create and immediately close a server to get a dead URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store, nil)
	server.SetWriteForwarder(NewWriteForwarder(deadURL))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when writer is down, got %d", rr.Code)
	}
}

func TestNoForwarder_NormalBehavior(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	// No forwarder set — legacy/writer mode.
	server := NewServer(nil, store, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 in normal mode, got %d", rr.Code)
	}
}
