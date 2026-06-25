package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func TestServeHTTPStatefulEndpoints(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	base := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	issue, _ := mustPersistProcessedEvent(t, store, worker.ProcessedEvent{
		Fingerprint: "fingerprint-stateful",
		Event: event.Event{
			ReceivedAt: base,
			ObservedAt: base,
			Severity:   "ERROR",
			Message:    "stateful error",
			Exception: event.Exception{
				Type:    "panic",
				Message: "stateful error",
			},
		},
	})

	server := NewServer(nil, store, nil)

	t.Run("resolve and reopen issue", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/issues/"+issue.ID+"/resolve", nil)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected resolve status: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/v1/issues/"+issue.ID+"/reopen", nil)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected reopen status: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
	})

	t.Run("release list and create", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name":"v1.2.3","environment":"staging","observedAt":"2026-04-15T12:00:00Z","version":"1.2.3","commitSha":"abc123","url":"https://example.com","notes":"deploy"}`)
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/releases", body)
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected release create status: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/api/v1/releases", nil)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected release list status: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
		var response struct {
			Releases []storage.Release `json:"releases"`
		}
		decodeResponse(t, rr, &response)
		if len(response.Releases) == 0 {
			t.Fatal("expected release in list")
		}
	})

	t.Run("alert create and settings", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name":"High errors","enabled":true,"severity":"error","condition":"count>10","query":"issue:123","target":"slack"}`)
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", body)
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected alert create status: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(`{"displayName":"BugBarn","timezone":"UTC","defaultEnvironment":"staging","liveWindowMinutes":15,"stacktraceContextLines":3}`))
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected settings status: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
	})

	t.Run("source map upload", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("release", "1.2.3")
		_ = writer.WriteField("bundle_url", "https://example.com/app.js")
		_ = writer.WriteField("source_map_name", "app.js.map")
		part, err := writer.CreateFormFile("source_map", "app.js.map")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(`{"version":3}`)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/source-maps", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("unexpected source map status: got %d want %d body=%q", rr.Code, http.StatusAccepted, rr.Body.String())
		}
	})
}

func TestServeHTTPUserAuthentication(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager("test-secret", time.Hour)
	server := NewServerWithAuth(nil, store, userAuth, sessions, nil, nil)

	t.Run("query endpoints require session", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	var session *http.Cookie
	t.Run("login sets session cookie", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"change-me"}`))
		req.Header.Set("Content-Type", "application/json")

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
		for _, cookie := range rr.Result().Cookies() {
			if cookie.Name == "bugbarn_session" {
				session = cookie
			}
		}
		if session == nil || session.Value == "" {
			t.Fatal("expected session cookie")
		}
	})

	t.Run("session can access query endpoint", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)
		req.AddCookie(session)

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
	})

	t.Run("wrong password is rejected", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusUnauthorized)
		}
	})
}
