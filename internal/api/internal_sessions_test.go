package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
)

const internalTestSecret = "internal-secret"

func newInternalSessionServer(t *testing.T) *Server {
	t.Helper()
	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })
	s := NewServer(nil, store, nil)
	s.SetSessionStore(sessionstore.NewDirect(store, nil))
	s.SetInternalSessionSecret(internalTestSecret)
	return s
}

func signedInternal(t *testing.T, s *Server, op string, req sessionstore.Request, sign bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, sessionstore.InternalPathPrefix+op, strings.NewReader(string(body)))
	if sign {
		r.Header.Set(sessionstore.AuthHeader, sessionstore.SignBody([]byte(internalTestSecret), body))
	}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, r)
	return rr
}

func TestInternalSessionsRefuseUnsignedAndStaleRequests(t *testing.T) {
	t.Parallel()
	s := newInternalSessionServer(t)
	now := time.Now().Unix()

	if rr := signedInternal(t, s, "delete", sessionstore.Request{TS: now, IDHash: "h"}, false); rr.Code != http.StatusForbidden {
		t.Errorf("unsigned = %d, want 403", rr.Code)
	}
	if rr := signedInternal(t, s, "delete", sessionstore.Request{TS: now - 3600, IDHash: "h"}, true); rr.Code != http.StatusForbidden {
		t.Errorf("stale timestamp = %d, want 403", rr.Code)
	}

	bad := httptest.NewRequest(http.MethodPost, sessionstore.InternalPathPrefix+"delete", strings.NewReader("not json"))
	bad.Header.Set(sessionstore.AuthHeader, sessionstore.SignBody([]byte(internalTestSecret), []byte("not json")))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, bad)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("signed non-JSON = %d, want 400", rr.Code)
	}
}

func TestInternalSessionsOperations(t *testing.T) {
	t.Parallel()
	s := newInternalSessionServer(t)
	now := time.Now().UTC()
	ws := domain.WebSession{
		IDHash: "hash-1", Username: "u", AuthMethod: domain.WebSessionAuthOIDC,
		IdpSub: "sub-1", IdpSid: "sid-1", CreatedAt: now, AbsoluteExpiresAt: now.Add(time.Hour),
	}

	if rr := signedInternal(t, s, "create", sessionstore.Request{TS: now.Unix()}, true); rr.Code != http.StatusBadRequest {
		t.Errorf("create without session = %d, want 400", rr.Code)
	}
	if rr := signedInternal(t, s, "create", sessionstore.Request{TS: now.Unix(), Session: &ws}, true); rr.Code != http.StatusOK {
		t.Fatalf("create = %d %q", rr.Code, rr.Body.String())
	}
	if rr := signedInternal(t, s, "get-or-refresh", sessionstore.Request{TS: now.Unix(), IDHash: "hash-1"}, true); rr.Code != http.StatusOK ||
		!strings.Contains(rr.Body.String(), "hash-1") {
		t.Errorf("get-or-refresh = %d %q, want the session", rr.Code, rr.Body.String())
	}
	if rr := signedInternal(t, s, "get-or-refresh", sessionstore.Request{TS: now.Unix(), IDHash: "missing"}, true); rr.Code != http.StatusNotFound {
		t.Errorf("get-or-refresh of an unknown session = %d, want 404", rr.Code)
	}

	rr := signedInternal(t, s, "delete-by-sid", sessionstore.Request{TS: now.Unix(), SID: "sid-1"}, true)
	var resp sessionstore.Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || rr.Code != http.StatusOK || resp.Deleted != 1 {
		t.Errorf("delete-by-sid = %d %q, want 1 deleted", rr.Code, rr.Body.String())
	}
	if rr := signedInternal(t, s, "delete-by-sub", sessionstore.Request{TS: now.Unix(), Sub: "sub-1"}, true); rr.Code != http.StatusOK {
		t.Errorf("delete-by-sub = %d", rr.Code)
	}
	if rr := signedInternal(t, s, "delete", sessionstore.Request{TS: now.Unix(), IDHash: "hash-1"}, true); rr.Code != http.StatusOK {
		t.Errorf("delete = %d", rr.Code)
	}
	if rr := signedInternal(t, s, "bogus", sessionstore.Request{TS: now.Unix()}, true); rr.Code != http.StatusNotFound {
		t.Errorf("unknown op = %d, want 404", rr.Code)
	}
}

func TestInternalSessionsAreDisabledWithoutSecret(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	s := NewServer(nil, store, nil)
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, httptest.NewRequest(method, sessionstore.InternalPathPrefix+"create", nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s while disabled = %d, want 404", method, rr.Code)
		}
	}
}
