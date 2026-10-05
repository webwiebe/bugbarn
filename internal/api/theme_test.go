package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeThemeManifest(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/iambarn-theme.json", nil)
	req.Header.Set("Accept", "application/json")

	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d (body=%q)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type: got %q want it to contain application/json", ct)
	}

	// Decode into a map so a field dropped from the Go struct shows up as
	// missing rather than as a zero value.
	var m map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	// The 13 fields of iambarn's theme.Manifest.
	for _, field := range []string{
		"name", "logo_url", "primary_color", "background_color", "card_color",
		"body_text_color", "support_url", "locale", "default_locale",
		"supported_locales", "from_address", "from_name", "dark",
	} {
		if v, ok := m[field]; !ok || v == "" {
			t.Errorf("field %q missing or empty", field)
		}
	}
	if got := m["from_address"]; got != "bugbarn@wiebe.xyz" {
		t.Errorf("from_address: got %v want bugbarn@wiebe.xyz", got)
	}
}
