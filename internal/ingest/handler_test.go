package ingest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func TestServeHTTPAcceptedAndSpoolsBody(t *testing.T) {
	dir := t.TempDir()
	eventSpool, err := spool.New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer eventSpool.Close()

	handler := NewHandler(auth.New("secret"), eventSpool, 1024)
	handler.idFn = func() string { return "ingest-123" }

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{"message":"boom"}`))
	req.Header.Set(auth.HeaderAPIKey, "secret")
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if accepted, ok := response["accepted"].(bool); !ok || !accepted {
		t.Fatalf("expected accepted true, got %#v", response["accepted"])
	}

	if ingestID, ok := response["ingestId"].(string); !ok || ingestID != "ingest-123" {
		t.Fatalf("expected ingestId ingest-123, got %#v", response["ingestId"])
	}

	raw := mustReadFile(t, filepath.Join(dir, spool.DefaultFileName))
	var record spool.Record
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode spool record: %v", err)
	}

	if record.IngestID != "ingest-123" {
		t.Fatalf("expected ingest id ingest-123, got %q", record.IngestID)
	}

	if got, want := record.BodyBase64, base64.StdEncoding.EncodeToString([]byte(`{"message":"boom"}`)); got != want {
		t.Fatalf("expected body %q, got %q", want, got)
	}
}

func TestServeHTTPRejectsMissingAPIKeyWhenEnabled(t *testing.T) {
	handler := NewHandler(auth.New("secret"), mustSpool(t), 1024)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestServeHTTPRejectsOversizeBody(t *testing.T) {
	handler := NewHandler(auth.New(""), mustSpool(t), 8)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader("this body is too long"))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rr.Code)
	}
}

func TestServeHTTPRejectsWrongMethod(t *testing.T) {
	handler := NewHandler(auth.New(""), mustSpool(t), 1024)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}

	if allow := rr.Header().Get("Allow"); allow != http.MethodPost {
		t.Fatalf("expected Allow POST, got %q", allow)
	}
}

func mustSpool(t *testing.T) *spool.Spool {
	t.Helper()

	dir := t.TempDir()
	eventSpool, err := spool.New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	t.Cleanup(func() {
		_ = eventSpool.Close()
	})
	return eventSpool
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return data
}
