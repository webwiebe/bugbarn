package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The deploy smoke compares this response with what it just rolled out, so the
// shape is a contract: three string fields, served without credentials.
func TestVersionEndpoint(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	srv := NewServer(nil, store, nil)
	srv.SetBuildInfo("v1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-10-17T12:00:00Z")

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without credentials", w.Code)
	}
	// The deploy smoke matches these exact substrings, so the encoding is part of
	// the contract.
	for _, want := range []string{`"version":"v1.2.3"`, `"commit":"0123456789abcdef0123456789abcdef01234567"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body %q lacks %s", w.Body.String(), want)
		}
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	want := map[string]string{
		"version": "v1.2.3",
		"commit":  "0123456789abcdef0123456789abcdef01234567",
		"built":   "2026-10-17T12:00:00Z",
	}
	if len(got) != len(want) {
		t.Errorf("got %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
