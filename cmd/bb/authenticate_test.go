package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// authenticate verifies an API key against the server before it stores it,
// and refuses to continue without a key or a username.
func TestAuthenticateAPIKeyVerifiesAgainstServer(t *testing.T) {
	t.Parallel()

	var path string
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"issues":[]}`))
	}))
	t.Cleanup(good.Close)

	cfg := Config{URL: good.URL}
	if err := authenticate(&cfg, "key-1", "", ""); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if cfg.Auth.Type != "apikey" || cfg.Auth.APIKey != "key-1" {
		t.Fatalf("Auth = %+v, want an apikey auth with key-1", cfg.Auth)
	}
	if path != "/api/v1/issues" {
		t.Fatalf("verified against %q, want /api/v1/issues", path)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(bad.Close)
	if err := authenticate(&Config{URL: bad.URL}, "wrong", "", ""); err == nil {
		t.Fatal("authenticate accepted a key the server rejects")
	}

	if err := authenticate(&Config{URL: good.URL}, "", "", ""); err == nil {
		t.Fatal("authenticate succeeded without a key or a username")
	}
}
