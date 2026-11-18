package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GET /api/v1/environments answers a list, empty when the project has no
// issue with an environment.
func TestEnvironmentsEndpoint(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/environments", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var got struct {
		Environments []map[string]any `json:"environments"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Environments == nil || len(got.Environments) != 0 {
		t.Fatalf("environments = %#v, want an empty list", got.Environments)
	}
}
