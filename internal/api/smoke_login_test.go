package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

func TestSmokeLoginGetsTheSameSessionAsTheAdmin(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte("smoke-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	userAuth, err = userAuth.WithSmokeLogin("e2e-bugbarn-smoke@e2e.wiebe.xyz", string(hash))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithAuth(nil, store, userAuth, auth.NewSessionManager("test-secret", time.Hour), nil, nil)

	login := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(rr, req)
		return rr
	}

	rr := login(`{"username":"e2e-bugbarn-smoke@e2e.wiebe.xyz","password":"smoke-pass"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("smoke login: got %d body=%q", rr.Code, rr.Body.String())
	}
	var payload struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Username != "e2e-bugbarn-smoke@e2e.wiebe.xyz" {
		t.Fatalf("the session must name the smoke user, got %q", payload.Username)
	}

	var session *http.Cookie
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == "bugbarn_session" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("expected a session cookie")
	}
	query := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)
	req.AddCookie(session)
	server.ServeHTTP(query, req)
	if query.Code != http.StatusOK {
		t.Fatalf("the smoke session must read like the admin one, got %d", query.Code)
	}

	if rr := login(`{"username":"e2e-bugbarn-smoke@e2e.wiebe.xyz","password":"wrong"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong smoke password: got %d want 401", rr.Code)
	}
	if rr := login(`{"username":"admin","password":"change-me"}`); rr.Code != http.StatusOK {
		t.Fatalf("admin must keep working: got %d", rr.Code)
	}
}
