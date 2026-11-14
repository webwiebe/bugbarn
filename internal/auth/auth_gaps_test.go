package auth_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

func TestNewHashedAcceptsEmptyAndRejectsBadDigests(t *testing.T) {
	t.Parallel()
	a, err := auth.NewHashed("  ")
	if err != nil || a.Enabled() {
		t.Fatalf("empty digest = %v enabled %v", err, a.Enabled())
	}
	if _, err := auth.NewHashed("zz"); err == nil {
		t.Fatal("non-hex digest accepted")
	}
	if _, err := auth.NewHashed("abcd"); err == nil {
		t.Fatal("short digest accepted")
	}
	// sha256("secret")
	a, err = auth.NewHashed("2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b")
	if err != nil || !a.Enabled() || !a.Valid("secret") || a.Valid("other") {
		t.Fatalf("hashed authorizer: err %v", err)
	}
}

func TestNilAuthorizerBuildersAndEnabled(t *testing.T) {
	t.Parallel()
	var nilAuth *auth.Authorizer
	if nilAuth.Enabled() {
		t.Fatal("nil authorizer is enabled")
	}
	lookup := func(context.Context, string) (int64, string, bool, error) { return 7, "ingest", true, nil }
	withDB := nilAuth.WithDBLookup(lookup, nil)
	if !withDB.Enabled() {
		t.Fatal("a database lookup enables the authorizer")
	}
	withSetup := nilAuth.WithSetupKeyVerifier(func(context.Context, string, string) (int64, bool, error) { return 1, true, nil })
	if withSetup.Enabled() {
		t.Fatal("a setup verifier alone does not enable the authorizer")
	}
	// Copies keep the other mechanisms.
	chained := auth.New("static").WithDBLookup(lookup, nil).WithSetupKeyVerifier(
		func(context.Context, string, string) (int64, bool, error) { return 9, true, nil })
	if !chained.Valid("static") {
		t.Fatal("static key lost after chaining")
	}
	if pid, _, ok := chained.ValidWithProject(context.Background(), "anything"); !ok || pid != 7 {
		t.Fatalf("db lookup lost after chaining: %d %v", pid, ok)
	}
}

func TestProjectForKeyErrReportsLookupFailures(t *testing.T) {
	t.Parallel()
	boom := errors.New("database is locked")
	failing := auth.New("").WithDBLookup(func(context.Context, string) (int64, string, bool, error) {
		return 0, "", false, boom
	}, nil)
	_, _, ok, err := failing.ProjectForKeyErr(context.Background(), "k")
	if ok || !errors.Is(err, auth.ErrKeyLookup) || !errors.Is(err, boom) {
		t.Fatalf("ok %v err %v, want ErrKeyLookup wrapping the cause", ok, err)
	}

	missing := auth.New("").WithDBLookup(func(context.Context, string) (int64, string, bool, error) {
		return 0, "", false, nil
	}, nil)
	if _, _, ok, err := missing.ProjectForKeyErr(context.Background(), "k"); ok || err != nil {
		t.Fatalf("unknown key: ok %v err %v", ok, err)
	}
	found := auth.New("").WithDBLookup(func(context.Context, string) (int64, string, bool, error) {
		return 3, "read", true, nil
	}, func(context.Context, string) error { t.Error("ProjectForKeyErr touched the key"); return nil })
	if pid, scope, ok, err := found.ProjectForKeyErr(context.Background(), "k"); !ok || err != nil || pid != 3 || scope != "read" {
		t.Fatalf("known key: %d %q %v %v", pid, scope, ok, err)
	}
}

func TestUserAuthenticatorUsername(t *testing.T) {
	t.Parallel()
	var none *auth.UserAuthenticator
	if none.Username() != "" {
		t.Fatal("nil authenticator has a username")
	}
	ua, err := auth.NewUserAuthenticator("admin", "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	if ua.Username() != "admin" {
		t.Fatalf("Username = %q", ua.Username())
	}
}

func TestSessionManagerTTLAndCookies(t *testing.T) {
	t.Parallel()
	var nilMgr *auth.SessionManager
	if nilMgr.TTL() != 12*time.Hour {
		t.Fatalf("nil TTL = %v", nilMgr.TTL())
	}
	if got := auth.NewSessionManager("s", 0).TTL(); got != 12*time.Hour {
		t.Fatalf("default TTL = %v", got)
	}
	m := auth.NewSessionManager("s", time.Hour)
	if m.TTL() != time.Hour {
		t.Fatalf("TTL = %v", m.TTL())
	}

	exp := time.Now().Add(time.Hour)
	sc := auth.SessionCookie("handle", exp, true)
	if sc.Name != "bugbarn_session" || sc.Value != "handle" || !sc.HttpOnly || !sc.Secure || sc.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", sc)
	}
	if cc := auth.ClearSessionCookie(false); cc.MaxAge != -1 || cc.Value != "" || cc.Name != "bugbarn_session" || cc.Secure {
		t.Fatalf("clear session cookie = %+v", cc)
	}

	csrf := m.CSRFCookie("handle", exp, true)
	if csrf.Name != "bugbarn_csrf" || csrf.Value != m.CSRFToken("handle") || csrf.HttpOnly || len(csrf.Value) != 32 {
		t.Fatalf("csrf cookie = %+v", csrf)
	}
	if m.CSRFToken("handle") == m.CSRFToken("other") {
		t.Fatal("csrf tokens do not depend on the session")
	}
	if cc := auth.ClearCSRFCookie(true); cc.MaxAge != -1 || cc.Value != "" || cc.Name != "bugbarn_csrf" || !cc.Secure {
		t.Fatalf("clear csrf cookie = %+v", cc)
	}
}

func TestOIDCClientAccessorsAndLogoutURL(t *testing.T) {
	t.Parallel()
	idp, client := newClient(t)
	cfg := client.Config()
	if cfg.ClientID != testClientID || cfg.RequiredGroup != "bugbarn-users" {
		t.Fatalf("Config = %+v", cfg)
	}
	if client.Issuer() != strings.TrimRight(idp.Issuer(), "/") {
		t.Fatalf("Issuer = %q", client.Issuer())
	}
	logout := client.LogoutURL()
	if end := client.EndSessionURL(); end == "" || !strings.HasPrefix(logout, end) {
		t.Fatalf("LogoutURL %q does not extend the end-session URL %q", logout, end)
	}
	for _, want := range []string{"client_id=" + testClientID, "post_logout_redirect_uri=https%3A%2F%2Fbugbarn.example.com%2F"} {
		if !strings.Contains(logout, want) {
			t.Errorf("LogoutURL %q lacks %q", logout, want)
		}
	}
}

func TestOIDCEndSessionURLIsEmptyWhenTheIssuerIsUnreachable(t *testing.T) {
	t.Parallel()
	client := auth.NewOIDCClient(auth.OIDCConfig{Issuer: "http://127.0.0.1:1", ClientID: "c", RedirectURL: "https://x.example/cb"})
	if got := client.EndSessionURL(); got != "" {
		t.Fatalf("EndSessionURL = %q, want empty", got)
	}
	if got := client.LogoutURL(); got != "" {
		t.Fatalf("LogoutURL = %q, want empty", got)
	}
}
