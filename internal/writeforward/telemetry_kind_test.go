package writeforward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

func TestKindForPathTelemetry(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		TelemetrySecurityPath:          queue.KindSecurity,
		TelemetryMetricsPath + "?x=1":  queue.KindMetrics,
		"/api/v1/telemetry/other":      "",
		"/api/v1/telemetry/security/x": "",
	} {
		if got := kindForPath(path); got != want {
			t.Errorf("kindForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// Readers spool telemetry into the Redis queue with its own kind; the
// consumer routes on kind and needs no project slug.
func TestRedisSpoolTelemetry(t *testing.T) {
	sf, q := newTestRedisForwarder(t)
	req := httptest.NewRequest(http.MethodPost, TelemetrySecurityPath, strings.NewReader("LINE"))
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("Forward status = %d, want 202", rec.Code)
	}
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	items, err := q.Consume(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != queue.KindSecurity {
		t.Fatalf("items = %+v, want one security item", items)
	}
}
