package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/ingesthealth"
)

func TestDetailedHealthReflectsIngestStall(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store, nil)

	t.Run("stalled ingest returns 503", func(t *testing.T) {
		server.SetIngestHealth(func() ingesthealth.Snapshot {
			return ingesthealth.Snapshot{
				Sampled: true,
				Healthy: false,
				Reasons: []string{"no event persisted for 120h0m0s (threshold 30m0s)"},
			}
		})

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health?detail=true", nil)
		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for stalled ingest, got %d", rr.Code)
		}
		var body struct {
			Status string `json:"status"`
			Ingest *struct {
				Healthy bool     `json:"healthy"`
				Reasons []string `json:"reasons"`
			} `json:"ingest"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Status != "unhealthy" {
			t.Fatalf("expected status unhealthy, got %q", body.Status)
		}
		if body.Ingest == nil || body.Ingest.Healthy {
			t.Fatalf("expected ingest report marked unhealthy, got %+v", body.Ingest)
		}
	})

	t.Run("healthy ingest returns 200", func(t *testing.T) {
		server.SetIngestHealth(func() ingesthealth.Snapshot {
			return ingesthealth.Snapshot{Sampled: true, Healthy: true, HasEvents: true}
		})

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health?detail=true", nil)
		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 for healthy ingest, got %d", rr.Code)
		}
	})
}
