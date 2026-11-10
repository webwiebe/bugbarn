package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingestresp"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// useReceivedReader swaps the received counter for one backed by a manual
// reader and returns a function that reads the count per outcome. The tests
// that call it cannot run in parallel.
func useReceivedReader(t *testing.T) func() map[string]int64 {
	t.Helper()
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	counter, err := provider.Meter("ingest-test").Int64Counter("bugbarn.ingest.received")
	if err != nil {
		t.Fatal(err)
	}
	previous := ingestReceivedCounter
	ingestReceivedCounter = counter
	t.Cleanup(func() {
		ingestReceivedCounter = previous
		_ = provider.Shutdown(context.Background())
	})
	return func() map[string]int64 {
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					continue
				}
				for _, dp := range sum.DataPoints {
					if v, ok := dp.Attributes.Value("outcome"); ok {
						out[v.AsString()] += dp.Value
					}
				}
			}
		}
		return out
	}
}

func newAlertmanagerHandler(t *testing.T) *Handler {
	t.Helper()
	eventSpool, err := spool.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eventSpool.Close() })
	return NewHandler(auth.New("secret"), eventSpool, 1024)
}

// bugbarn#270 (H3): Alertmanager traffic was missing from the received counter.
func TestAlertmanagerCountsADeniedKey(t *testing.T) {
	received := useReceivedReader(t)
	handler := newAlertmanagerHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alertmanager", strings.NewReader(`{"alerts":[]}`))
	req.Header.Set(auth.HeaderAPIKey, "wrong")
	rr := httptest.NewRecorder()

	handler.ServeAlertmanagerHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if got := received()[ingestresp.DropUnauthorized.Reason]; got != 1 {
		t.Errorf("received{outcome=%q} = %d, want 1", ingestresp.DropUnauthorized.Reason, got)
	}
}

func TestAlertmanagerCountsAcceptedAndMalformed(t *testing.T) {
	received := useReceivedReader(t)
	handler := newAlertmanagerHandler(t)
	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/alertmanager", strings.NewReader(body))
		req.Header.Set(auth.HeaderAPIKey, "secret")
		rr := httptest.NewRecorder()
		handler.ServeAlertmanagerHTTP(rr, req)
		return rr.Code
	}

	if code := post(`{"alerts":[{"labels":{"alertname":"A"},"annotations":{"summary":"s"}}]}`); code != http.StatusAccepted {
		t.Fatalf("valid envelope status = %d, want 202", code)
	}
	if code := post(`{"status":"firing"}`); code != http.StatusBadRequest {
		t.Fatalf("envelope without alerts status = %d, want 400", code)
	}

	got := received()
	if got["accepted"] != 1 {
		t.Errorf("received{outcome=accepted} = %d, want 1", got["accepted"])
	}
	if got[ingestresp.DropMalformed.Reason] != 1 {
		t.Errorf("received{outcome=%q} = %d, want 1", ingestresp.DropMalformed.Reason, got[ingestresp.DropMalformed.Reason])
	}
}
