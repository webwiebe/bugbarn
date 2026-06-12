package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/metric"
)

// TestInitExposesMetrics verifies the Prometheus pull endpoint is wired and that
// instruments created from the global meter show up in the exposition, with no
// OTLP backend configured. This is the pull-based "properly instrumented" path
// used when there is no metrics collector to push to.
func TestInitExposesMetrics(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "") // no traces backend; metrics still on.

	shutdown, err := Init(context.Background(), "test")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer shutdown(context.Background())

	h := MetricsHandler()
	if h == nil {
		t.Fatal("MetricsHandler() = nil, want a Prometheus handler")
	}

	// Record through the same path the consumer uses.
	ctr, err := Meter().Int64Counter("bugbarn.test.records")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	ctr.Add(context.Background(), 3, metric.WithAttributes())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "bugbarn_test_records") {
		t.Errorf("scrape missing instrument; body:\n%s", body)
	}
}
