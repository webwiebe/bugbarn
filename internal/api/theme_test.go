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

	type manifest struct {
		Name            string `json:"name"`
		LogoURL         string `json:"logo_url"`
		PrimaryColor    string `json:"primary_color"`
		BackgroundColor string `json:"background_color"`
		CardColor       string `json:"card_color"`
		BodyTextColor   string `json:"body_text_color"`
		SupportURL      string `json:"support_url"`
		Locale          string `json:"locale"`
	}
	var m manifest
	if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.Name == "" {
		t.Errorf("name should be populated, got empty")
	}
}
