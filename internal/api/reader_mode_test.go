package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/oidctest"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// readerHarness models the production CQRS split in-process: a writer Server
// over the read-write SQLite (internal session endpoints enabled) and a
// reader Server over a read-only mount of the SAME file, wired with the
// Remote session store and the write forwarder.
type readerHarness struct {
	writerStore *storage.Store
	writerHTTP  *httptest.Server
	reader      *Server
	idp         *oidctest.IdP
}

func newReaderHarness(t *testing.T) *readerHarness {
	t.Helper()
	const secret = "shared-session-secret"
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")

	idp, err := oidctest.New("bugbarn-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)
	oidcCfg := auth.OIDCConfig{
		Issuer:        idp.Issuer(),
		ClientID:      "bugbarn-test",
		ClientSecret:  "sek",
		RedirectURL:   "http://bugbarn.example.com/api/v1/oidc/callback",
		RequiredGroup: "bugbarn-users",
	}
	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}

	writerStore, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writerStore.Close() })
	writer := NewServerWithAuth(nil, writerStore, userAuth, auth.NewSessionManager(secret, 12*time.Hour), nil, nil)
	writer.SetOIDCClient(auth.NewOIDCClient(oidcCfg))
	writer.SetInternalSessionSecret(secret)
	writerHTTP := httptest.NewServer(writer)
	t.Cleanup(writerHTTP.Close)

	readerStore, err := storage.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { readerStore.Close() })
	reader := NewServerWithAuth(nil, readerStore, userAuth, auth.NewSessionManager(secret, 12*time.Hour), nil, nil)
	reader.SetOIDCClient(auth.NewOIDCClient(oidcCfg))
	reader.SetSessionStore(sessionstore.NewRemote(readerStore, writerHTTP.URL, secret))
	reader.SetWriteForwarder(NewWriteForwarder(writerHTTP.URL))

	return &readerHarness{writerStore: writerStore, writerHTTP: writerHTTP, reader: reader, idp: idp}
}

func TestReaderModeLoginAndValidate(t *testing.T) {
	h := newReaderHarness(t)

	// POST /api/v1/login on the reader is forwarded to the writer, which
	// persists the session row and sets the cookie.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login",
		strings.NewReader(`{"username":"admin","password":"change-me"}`))
	req.Header.Set("Content-Type", "application/json")
	h.reader.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reader login = %d body=%q", rr.Code, rr.Body.String())
	}
	var session *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "bugbarn_session" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("expected session cookie from forwarded login")
	}

	// Validation on the reader reads the shared SQLite file directly.
	if rr := readerGet(h.reader, "/api/v1/me", session); rr.Code != http.StatusOK {
		t.Fatalf("reader me = %d body=%q", rr.Code, rr.Body.String())
	}
	if _, err := h.writerStore.GetWebSession(t.Context(), auth.HashSessionHandle(session.Value)); err != nil {
		t.Fatalf("row must exist on the writer: %v", err)
	}
}

func TestReaderModeRefreshDelegatesToWriter(t *testing.T) {
	h := newReaderHarness(t)
	now := time.Now().UTC()

	handle := auth.NewSessionHandle()
	if err := h.writerStore.InsertWebSession(t.Context(), storage.WebSession{
		IDHash: auth.HashSessionHandle(handle), Username: "alice",
		AuthMethod: storage.WebSessionAuthOIDC,
		IdpSub:     "sub-1", IdpSid: "sid-1", RefreshToken: "rt-1",
		AccessExpiresAt: now.Add(-time.Minute),
		CreatedAt:       now, AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	freshID := h.idp.SignJWT(h.idp.IDTokenClaims("sub-1", "sid-1", "", []string{"bugbarn-users"}))
	h.idp.SetTokenHandler(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostFormValue("refresh_token") != "rt-1" {
			oidctest.WriteTokenError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		oidctest.WriteTokenResponse(w, freshID, "at-2", "rt-2", 900)
	})

	cookie := &http.Cookie{Name: "bugbarn_session", Value: handle}
	if rr := readerGet(h.reader, "/api/v1/me", cookie); rr.Code != http.StatusOK {
		t.Fatalf("reader me with expired access token = %d body=%q", rr.Code, rr.Body.String())
	}
	// The rotation happened on the writer and is visible in the shared file.
	ws, err := h.writerStore.GetWebSession(t.Context(), auth.HashSessionHandle(handle))
	if err != nil {
		t.Fatal(err)
	}
	if ws.RefreshToken != "rt-2" || ws.AccessToken != "at-2" {
		t.Errorf("rotation not persisted on writer: %+v", ws)
	}

	// invalid_grant on a second session → 401 via the remote store.
	deadHandle := auth.NewSessionHandle()
	if err := h.writerStore.InsertWebSession(t.Context(), storage.WebSession{
		IDHash: auth.HashSessionHandle(deadHandle), Username: "bob",
		AuthMethod: storage.WebSessionAuthOIDC, IdpSub: "sub-2", RefreshToken: "rt-dead",
		AccessExpiresAt: now.Add(-time.Minute),
		CreatedAt:       now, AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if rr := readerGet(h.reader, "/api/v1/me", &http.Cookie{Name: "bugbarn_session", Value: deadHandle}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("reader me after invalid_grant = %d", rr.Code)
	}
}

func TestReaderModeBackchannelForwarded(t *testing.T) {
	h := newReaderHarness(t)
	now := time.Now().UTC()
	handle := auth.NewSessionHandle()
	if err := h.writerStore.InsertWebSession(t.Context(), storage.WebSession{
		IDHash: auth.HashSessionHandle(handle), Username: "alice",
		AuthMethod: storage.WebSessionAuthOIDC,
		IdpSub:     "sub-1", IdpSid: "sid-1", RefreshToken: "rt-1",
		AccessExpiresAt: now.Add(15 * time.Minute),
		CreatedAt:       now, AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"logout_token": {h.idp.LogoutToken("sub-1", "sid-1", nil)}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/oidc/backchannel-logout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.reader.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reader backchannel = %d body=%q", rr.Code, rr.Body.String())
	}
	if rr := readerGet(h.reader, "/api/v1/me", &http.Cookie{Name: "bugbarn_session", Value: handle}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("session must be dead cluster-wide, reader me = %d", rr.Code)
	}
}

func TestInternalSessionEndpointsRejectUnsigned(t *testing.T) {
	h := newReaderHarness(t)
	// Direct, unsigned POST against the writer must be refused.
	res, err := http.Post(h.writerHTTP.URL+sessionstore.InternalPathPrefix+"get-or-refresh",
		"application/json", strings.NewReader(`{"ts":1,"id_hash":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("unsigned internal call = %d, want 403", res.StatusCode)
	}
}

func readerGet(server *Server, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	server.ServeHTTP(rr, req)
	return rr
}
