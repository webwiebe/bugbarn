package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func TestAlertmanagerEventsMapsEachAlert(t *testing.T) {
	payloads, err := AlertmanagerEvents([]byte(`{
  "status":"firing",
  "alerts":[
    {"status":"firing","labels":{"alertname":"KubeJobFailed","severity":"warning","namespace":"test"},"annotations":{"summary":"Job failed","description":"details"},"startsAt":"2026-08-17T12:00:00Z","generatorURL":"https://prometheus/graph","fingerprint":"abc"},
    {"status":"resolved","labels":{"alertname":"KubeMemoryOvercommit","severity":"critical"},"annotations":{},"startsAt":"2026-08-17T12:01:00Z","fingerprint":"def"}
  ]
}`))
	if err != nil {
		t.Fatalf("AlertmanagerEvents: %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("got %d events, want 2", len(payloads))
	}
	var first, second map[string]any
	if err := json.Unmarshal(payloads[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payloads[1], &second); err != nil {
		t.Fatal(err)
	}
	if got, want := first["body"], "KubeJobFailed: Job failed"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got, want := first["severityText"], "warning"; got != want {
		t.Fatalf("severity = %q, want %q", got, want)
	}
	if got, want := first["fingerprint"], "alertmanager:abc"; got != want {
		t.Fatalf("fingerprint = %q, want %q", got, want)
	}
	if got, want := second["body"], "KubeMemoryOvercommit"; got != want {
		t.Fatalf("resolved body = %q, want %q", got, want)
	}
	if got, want := second["severityText"], "info"; got != want {
		t.Fatalf("resolved severity = %q, want %q", got, want)
	}
}

func TestServeAlertmanagerHTTPSpoolsOneRecordPerAlert(t *testing.T) {
	dir := t.TempDir()
	eventSpool, err := spool.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eventSpool.Close()
	handler := NewHandler(auth.New("secret"), eventSpool, 1024)
	handler.idFn = func() string { return "alert-ingest" }
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alertmanager", strings.NewReader(`{"alerts":[{"labels":{"alertname":"A"},"annotations":{"summary":"first"}},{"labels":{"alertname":"B"},"annotations":{"summary":"second"}}]}`))
	req.Header.Set(auth.HeaderAPIKey, "secret")
	rr := httptest.NewRecorder()
	handler.ServeAlertmanagerHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rr.Code)
	}
	records, err := spool.ReadRecords(filepath.Join(dir, spool.DefaultFileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("spooled %d records, want 2", len(records))
	}
}

func TestAlertmanagerEventsRejectsEnvelopeWithoutAlerts(t *testing.T) {
	if _, err := AlertmanagerEvents([]byte(`{"status":"firing"}`)); err == nil {
		t.Fatal("expected malformed envelope to be rejected")
	}
}
