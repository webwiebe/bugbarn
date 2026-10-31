package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/api"
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const revocationSecret = "session-secret"

// revocationStack is the full stack the way main wires it: the real authorizer
// with the setup fallback, a real store and the API behind a signed-in admin.
type revocationStack struct {
	t        *testing.T
	store    *storage.Store
	handler  *ingest.Handler
	server   *api.Server
	sessions *auth.SessionManager
	session  *http.Cookie
}

func newRevocationStack(t *testing.T) *revocationStack {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.CreateProject(context.Background(), "Alpha", "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(context.Background(), "Beta", "beta"); err != nil {
		t.Fatal(err)
	}
	eventSpool, err := spool.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eventSpool.Close() })

	authorizer, err := newAPIAuthorizer(config.Config{SessionSecret: revocationSecret}, store, store.TouchAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	handler := ingest.NewHandler(authorizer, eventSpool, 1<<20)
	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager(revocationSecret, time.Hour)
	server := api.NewServerWithAuth(handler, store, userAuth, sessions, nil, nil)
	server.SetSetupConfig(revocationSecret, "https://bugs.example.com")

	s := &revocationStack{t: t, store: store, handler: handler, server: server, sessions: sessions}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"change-me"}`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)
	for _, c := range rr.Result().Cookies() {
		if c.Name == "bugbarn_session" {
			s.session = c
		}
	}
	if s.session == nil {
		t.Fatalf("no session cookie: %d", rr.Code)
	}
	return s
}

func (s *revocationStack) admin(method, path, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(s.session)
	req.Header.Set("X-BugBarn-CSRF", s.sessions.CSRFToken(s.session.Value))
	rr := httptest.NewRecorder()
	s.server.ServeHTTP(rr, req)
	return rr
}

func (s *revocationStack) withKey(method, path, key, project string) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(`{"name":"v1","environment":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.HeaderAPIKey, key)
	if project != "" {
		req.Header.Set("X-BugBarn-Project", project)
	}
	rr := httptest.NewRecorder()
	s.server.ServeHTTP(rr, req)
	return rr
}

// accepted runs the key through the same check every ingest route uses.
func (s *revocationStack) accepted(key, project string) bool {
	s.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", http.NoBody)
	req.Header.Set(auth.HeaderAPIKey, key)
	req.Header.Set("X-BugBarn-Project", project)
	_, _, ok, err := s.handler.CheckAPIKey(req)
	if err != nil {
		s.t.Fatalf("CheckAPIKey: %v", err)
	}
	return ok
}

// giveRow creates the row the setup page creates for a signed-in admin.
func (s *revocationStack) giveRow(slug string) {
	s.t.Helper()
	giveSetupRow(s.t, s.store, slug)
}

func giveSetupRow(t *testing.T, store *storage.Store, slug string) {
	t.Helper()
	ctx := context.Background()
	proj, err := store.ProjectBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSetupAPIKey(ctx, slug+"-setup", proj.ID, sha256Hex(setupKey(revocationSecret, slug))); err != nil {
		t.Fatal(err)
	}
}

// listing returns the API key names and revoked slugs the dashboard sees.
func (s *revocationStack) listing() (keys map[string]int64, revoked []string) {
	s.t.Helper()
	rr := s.admin(http.MethodGet, "/api/v1/apikeys", "")
	if rr.Code != http.StatusOK {
		s.t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		APIKeys []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"apiKeys"`
		Revoked []struct {
			Slug string `json:"slug"`
		} `json:"revokedSetupKeys"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		s.t.Fatal(err)
	}
	keys = map[string]int64{}
	for _, k := range out.APIKeys {
		keys[k.Name] = k.ID
	}
	for _, r := range out.Revoked {
		revoked = append(revoked, r.Slug)
	}
	return keys, revoked
}

func TestSetupKeyWithARowStopsWorkingWhenItsRowIsDeleted(t *testing.T) {
	s := newRevocationStack(t)
	key := setupKey(revocationSecret, "alpha")

	if !s.accepted(key, "alpha") {
		t.Fatal("the setup key must work through the fallback")
	}
	s.giveRow("alpha")
	keys, _ := s.listing()
	id, listed := keys["alpha-setup"]
	if !listed {
		t.Fatalf("the row must show in the listing, got %v", keys)
	}

	if rr := s.admin(http.MethodDelete, "/api/v1/apikeys/"+jsonNumber(id), ""); rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if s.accepted(key, "alpha") {
		t.Fatal("the deleted setup key must stop working, by the row and by the fallback")
	}
	keys, revoked := s.listing()
	if _, listed := keys["alpha-setup"]; listed {
		t.Fatalf("the row came back: %v", keys)
	}
	if len(revoked) != 1 || revoked[0] != "alpha" {
		t.Fatalf("revoked = %v, want alpha", revoked)
	}
}

func TestSetupKeyWithoutARowCanBeRevoked(t *testing.T) {
	s := newRevocationStack(t)
	key := setupKey(revocationSecret, "beta")
	other := setupKey(revocationSecret, "alpha")

	if rr := s.admin(http.MethodPost, "/api/v1/apikeys/revoke-setup", `{"slug":"beta"}`); rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rr.Code, rr.Body.String())
	}
	if s.accepted(key, "beta") {
		t.Fatal("a revoked setup key must stop working")
	}
	if !s.accepted(other, "alpha") {
		t.Fatal("another project's setup key must keep working")
	}
}

func TestSetupPageDoesNotBringBackARevokedSlug(t *testing.T) {
	s := newRevocationStack(t)
	key := setupKey(revocationSecret, "alpha")
	if rr := s.admin(http.MethodPost, "/api/v1/apikeys/revoke-setup", `{"slug":"alpha"}`); rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rr.Code, rr.Body.String())
	}

	// With an admin session, the one caller the page otherwise serves a key to.
	rr := s.admin(http.MethodGet, "/api/v1/setup/alpha", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("setup page: got %d, want 200", rr.Code)
	}
	if keys, _ := s.listing(); len(keys) != 0 {
		t.Fatalf("the page inserted a row: %v", keys)
	}
	if s.accepted(key, "alpha") {
		t.Fatal("the key must stay revoked after the setup page")
	}
}

func TestRevokingASetupKeyTakesASession(t *testing.T) {
	s := newRevocationStack(t)
	ctx := context.Background()
	alpha, err := s.store.ProjectBySlug(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateAPIKey(ctx, "full", alpha.ID, sha256Hex("a-full-key"), storage.APIKeyScopeFull); err != nil {
		t.Fatal(err)
	}
	if rr := s.withKey(http.MethodPost, "/api/v1/apikeys/revoke-setup", "a-full-key", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("full-scope key: got %d, want 403", rr.Code)
	}
	if !s.accepted(setupKey(revocationSecret, "beta"), "beta") {
		t.Fatal("the refused call must not have revoked anything")
	}
}

// An ordinary key that happens to be named like a setup key is only deleted: the
// hash decides, so no project's setup key is revoked by accident.
func TestDeletingAKeyNamedLikeASetupKeyRevokesNothing(t *testing.T) {
	s := newRevocationStack(t)
	ctx := context.Background()
	alpha, err := s.store.ProjectBySlug(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.store.CreateAPIKey(ctx, "alpha-setup", alpha.ID, sha256Hex("not-derived"), storage.APIKeyScopeIngest)
	if err != nil {
		t.Fatal(err)
	}
	if rr := s.admin(http.MethodDelete, "/api/v1/apikeys/"+jsonNumber(row.ID), ""); rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if !s.accepted(setupKey(revocationSecret, "alpha"), "alpha") {
		t.Fatal("the setup key must still work")
	}
	if _, revoked := s.listing(); len(revoked) != 0 {
		t.Fatalf("revoked = %v, want none", revoked)
	}
}

func TestDeletingAnUnknownKeyAnswers404(t *testing.T) {
	s := newRevocationStack(t)
	if rr := s.admin(http.MethodDelete, "/api/v1/apikeys/999", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rr.Code)
	}
}

func TestSetupFallbackReportsAFailedRevocationRead(t *testing.T) {
	failure := errors.New("database is locked")
	verify := withSetupKeyRevocation(newSetupKeyVerifier(revocationSecret), func(context.Context, string) (bool, error) {
		return false, failure
	})
	a := auth.New("").
		WithDBLookup(func(context.Context, string) (int64, string, bool, error) { return 0, "", false, nil }, nil).
		WithSetupKeyVerifier(verify)
	_, _, ok, err := a.CheckWithSetupFallback(context.Background(), setupKey(revocationSecret, "alpha"), "alpha")
	if ok || !errors.Is(err, auth.ErrKeyLookup) {
		t.Fatalf("ok=%v err=%v, want a retryable ErrKeyLookup", ok, err)
	}
}

// Ingest keeps release markers, confined to the key's own project. A setup key
// verifies against the slug it names, and once it has a row it is a bound key.
func TestDerivedKeyMarkersStayInItsOwnProject(t *testing.T) {
	s := newRevocationStack(t)
	key := setupKey(revocationSecret, "alpha")
	const releases = "/api/v1/releases"

	if rr := s.withKey(http.MethodPost, releases, key, "alpha"); rr.Code != http.StatusAccepted {
		t.Fatalf("own project: got %d, want 202: %s", rr.Code, rr.Body.String())
	}
	if rr := s.withKey(http.MethodPost, releases, key, "beta"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("another project without a row: got %d, want 401", rr.Code)
	}
	if rr := s.withKey(http.MethodPost, releases, key, "brand-new"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("an unknown project without a row: got %d, want 401", rr.Code)
	}

	s.giveRow("alpha")
	if rr := s.withKey(http.MethodPost, releases, key, "alpha"); rr.Code != http.StatusAccepted {
		t.Fatalf("own project with a row: got %d, want 202: %s", rr.Code, rr.Body.String())
	}
	if rr := s.withKey(http.MethodPost, releases, key, "beta"); rr.Code != http.StatusForbidden {
		t.Fatalf("another project with a row: got %d, want 403", rr.Code)
	}
	if rr := s.withKey(http.MethodPost, releases, key, "brand-new"); rr.Code != http.StatusForbidden {
		t.Fatalf("an unknown project with a row: got %d, want 403", rr.Code)
	}
	if _, err := s.store.ProjectBySlug(context.Background(), "brand-new"); err == nil {
		t.Fatal("a refused marker created a project")
	}
}

func jsonNumber(id int64) string { return strconv.FormatInt(id, 10) }

func sha256Hex(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
