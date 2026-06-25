package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

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
	server := NewServerWithAuth(ingestHandler, store, userAuth, sessions, nil, nil)

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

func TestReadOnlyKeyScope(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	// Generate a plaintext key and store it as read-scope.
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
	if _, err := store.CreateAPIKey(context.Background(), "test-read-key", proj.ID, keySHA256, storage.APIKeyScopeRead); err != nil {
		t.Fatal(err)
	}

	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	ingestHandler := ingest.NewHandler(authorizer, nil, 1<<20)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	sessions := auth.NewSessionManager("secret", time.Hour)
	server := NewServerWithAuth(ingestHandler, store, userAuth, sessions, nil, nil)

	t.Run("read-only key can GET protected endpoints", func(t *testing.T) {
		for _, path := range []string{"/api/v1/issues", "/api/v1/releases", "/api/v1/projects", "/api/v1/logs"} {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("x-bugbarn-api-key", plaintext)
			server.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("GET %s: got %d want %d body=%q", path, rr.Code, http.StatusOK, rr.Body.String())
			}
		}
	})

	t.Run("read-only key is blocked from POST/PUT/DELETE", func(t *testing.T) {
		methods := []struct {
			method string
			path   string
		}{
			{http.MethodPost, "/api/v1/releases"},
			{http.MethodDelete, "/api/v1/projects/default"},
			{http.MethodPut, "/api/v1/settings"},
		}
		for _, tc := range methods {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			req.Header.Set("x-bugbarn-api-key", plaintext)
			req.Header.Set("Content-Type", "application/json")
			server.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s %s: got %d want %d", tc.method, tc.path, rr.Code, http.StatusForbidden)
			}
		}
	})

	t.Run("read-only key is blocked from settings and apikeys", func(t *testing.T) {
		for _, path := range []string{"/api/v1/settings", "/api/v1/apikeys"} {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("x-bugbarn-api-key", plaintext)
			server.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("GET %s: got %d want %d", path, rr.Code, http.StatusForbidden)
			}
		}
	})
}

func TestIngestCORSHeaders(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

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
