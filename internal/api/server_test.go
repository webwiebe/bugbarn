package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func TestServeHTTPQueryEndpoints(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	base := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	issueA1, eventA1 := mustPersistProcessedEvent(t, store, worker.ProcessedEvent{
		Fingerprint: "fingerprint-a",
		Event: event.Event{
			ReceivedAt: base,
			ObservedAt: base,
			Severity:   "ERROR",
			Message:    "request failed for user 12345",
			Exception: event.Exception{
				Type:    "panic",
				Message: "request failed for user 12345",
			},
			Attributes: map[string]any{
				"service": "api",
			},
		},
	})
	issueA2, eventA2 := mustPersistProcessedEvent(t, store, worker.ProcessedEvent{
		Fingerprint: "fingerprint-a",
		Event: event.Event{
			ReceivedAt: base.Add(5 * time.Minute),
			ObservedAt: base.Add(5 * time.Minute),
			Severity:   "ERROR",
			Message:    "request failed for user 67890",
			Exception: event.Exception{
				Type:    "panic",
				Message: "request failed for user 67890",
			},
			Attributes: map[string]any{
				"service": "api",
			},
		},
	})
	if issueA2.ID != issueA1.ID {
		t.Fatalf("expected repeated fingerprint to reuse the same issue, got %q and %q", issueA1.ID, issueA2.ID)
	}
	issueB, eventB := mustPersistProcessedEvent(t, store, worker.ProcessedEvent{
		Fingerprint: "fingerprint-b",
		Event: event.Event{
			ReceivedAt: base.Add(10 * time.Minute),
			ObservedAt: base.Add(10 * time.Minute),
			Severity:   "ERROR",
			Message:    "background job failed",
			Exception: event.Exception{
				Type:    "panic",
				Message: "background job failed",
			},
			Attributes: map[string]any{
				"service": "worker",
			},
		},
	})

	server := NewServer(nil, store)

	t.Run("list issues", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusOK)
		}

		var response struct {
			Issues []storage.Issue `json:"issues"`
		}
		decodeResponse(t, rr, &response)

		if got, want := len(response.Issues), 2; got != want {
			t.Fatalf("unexpected issue count: got %d want %d", got, want)
		}
		if got, want := response.Issues[0].ID, issueB.ID; got != want {
			t.Fatalf("unexpected first issue: got %q want %q", got, want)
		}
		if got, want := response.Issues[1].ID, issueA1.ID; got != want {
			t.Fatalf("unexpected second issue: got %q want %q", got, want)
		}
	})

	t.Run("issue detail", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/"+issueA1.ID, nil)

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusOK)
		}

		var response struct {
			Issue storage.Issue `json:"issue"`
		}
		decodeResponse(t, rr, &response)

		if got, want := response.Issue.ID, issueA1.ID; got != want {
			t.Fatalf("unexpected issue id: got %q want %q", got, want)
		}
		if got, want := response.Issue.EventCount, 2; got != want {
			t.Fatalf("unexpected event count: got %d want %d", got, want)
		}
	})

	t.Run("issue events", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/"+issueA1.ID+"/events", nil)

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusOK)
		}

		var response struct {
			Events []storage.Event `json:"events"`
		}
		decodeResponse(t, rr, &response)

		if got, want := len(response.Events), 2; got != want {
			t.Fatalf("unexpected event count: got %d want %d", got, want)
		}
		if got, want := response.Events[0].ID, eventA1.ID; got != want {
			t.Fatalf("unexpected first event: got %q want %q", got, want)
		}
		if got, want := response.Events[1].ID, eventA2.ID; got != want {
			t.Fatalf("unexpected second event: got %q want %q", got, want)
		}
	})

	t.Run("event detail", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+eventA2.ID, nil)

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusOK)
		}

		var response struct {
			Event storage.Event `json:"event"`
		}
		decodeResponse(t, rr, &response)

		if got, want := response.Event.ID, eventA2.ID; got != want {
			t.Fatalf("unexpected event id: got %q want %q", got, want)
		}
		if got, want := response.Event.IssueID, issueA1.ID; got != want {
			t.Fatalf("unexpected issue id: got %q want %q", got, want)
		}
	})

	t.Run("live events", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/live/events?since=2026-04-15T11:45:00Z", nil)

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusOK)
		}

		var response struct {
			Events []storage.Event `json:"events"`
		}
		decodeResponse(t, rr, &response)

		if got, want := len(response.Events), 3; got != want {
			t.Fatalf("unexpected event count: got %d want %d", got, want)
		}
		if got, want := response.Events[0].ID, eventB.ID; got != want {
			t.Fatalf("unexpected first live event: got %q want %q", got, want)
		}
		if got, want := response.Events[1].ID, eventA2.ID; got != want {
			t.Fatalf("unexpected second live event: got %q want %q", got, want)
		}
		if got, want := response.Events[2].ID, eventA1.ID; got != want {
			t.Fatalf("unexpected third live event: got %q want %q", got, want)
		}
	})
}

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

	server := NewServer(nil, store)

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
	server := NewServerWithAuth(nil, store, userAuth, sessions, nil)

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

func TestIngestOnlyKeyScope(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	// Generate a plaintext key and store it as ingest-scope.
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	plaintext := hex.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(plaintext))
	keySHA256 := hex.EncodeToString(sum[:])

	proj, err := store.ProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), "test-ingest-key", proj.ID, keySHA256, storage.APIKeyScopeIngest); err != nil {
		t.Fatal(err)
	}

	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	ingestHandler := ingest.NewHandler(authorizer, nil, 1<<20)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	sessions := auth.NewSessionManager("secret", time.Hour)
	server := NewServerWithAuth(ingestHandler, store, userAuth, sessions, nil)

	t.Run("ingest-only key is blocked from protected endpoints", func(t *testing.T) {
		for _, path := range []string{"/api/v1/issues", "/api/v1/releases", "/api/v1/settings", "/api/v1/apikeys"} {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("x-bugbarn-api-key", plaintext)
			server.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("path %s: got %d want %d", path, rr.Code, http.StatusForbidden)
			}
		}
	})

	t.Run("full-scope key can access protected endpoints", func(t *testing.T) {
		var rawFull [32]byte
		if _, err := rand.Read(rawFull[:]); err != nil {
			t.Fatal(err)
		}
		fullPlain := hex.EncodeToString(rawFull[:])
		sumFull := sha256.Sum256([]byte(fullPlain))
		if _, err := store.CreateAPIKey(context.Background(), "full-key", proj.ID, hex.EncodeToString(sumFull[:]), storage.APIKeyScopeFull); err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)
		req.Header.Set("x-bugbarn-api-key", fullPlain)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("full key /api/v1/issues: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
	})
}

func TestIngestCORSHeaders(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store)

	t.Run("OPTIONS preflight returns wildcard ACAO", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/events", nil)
		req.Header.Set("Origin", "https://app.example.com")
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("got %d want 204", rr.Code)
		}
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("ACAO: got %q want %q", got, "*")
		}
	})

	t.Run("POST ingest returns wildcard ACAO", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://app.example.com")
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(rr, req)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("ACAO on POST: got %q want %q", got, "*")
		}
	})

	t.Run("non-ingest endpoint does not get wildcard ACAO", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)
		req.Header.Set("Origin", "https://app.example.com")
		server.ServeHTTP(rr, req)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got == "*" {
			t.Error("expected non-wildcard ACAO on protected endpoint, got *")
		}
	})
}

func mustOpenStore(t *testing.T) *storage.Store {
	t.Helper()

	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustPersistProcessedEvent(t *testing.T, store *storage.Store, processed worker.ProcessedEvent) (storage.Issue, storage.Event) {
	t.Helper()

	issue, eventRow, err := store.PersistProcessedEvent(context.Background(), processed)
	if err != nil {
		t.Fatal(err)
	}
	return issue, eventRow
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder, dest any) {
	t.Helper()

	if got, want := rr.Header().Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("unexpected content type: got %q want %q", got, want)
	}
	if err := json.NewDecoder(rr.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}
