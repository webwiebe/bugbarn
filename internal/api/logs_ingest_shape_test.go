package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func postLogs(t *testing.T, server *Server, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(domain.WithProjectID(req.Context(), 1))
	server.serveLogsIngest(rr, req)
	return rr
}

// A JSON log body whose "logs" key is missing, null or not an array answered
// 204 and stored nothing, so a sender with the wrong shape never learned (#263).
// It now answers 400 and names the shape.
func TestServeLogsIngestRefusesBodyWithoutLogsArray(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	for _, body := range []string{`{}`, `{"logs":null}`, `null`, `{"message":"bare"}`} {
		rr := postLogs(t, server, "application/json", body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d want 400 body=%q", body, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"logs" array`) {
			t.Errorf("%s: body %q does not name the expected shape", body, rr.Body.String())
		}
	}
	for _, body := range []string{`{"logs":"x"}`, `{"logs":{}}`, `[{"message":"bare"}]`} {
		if rr := postLogs(t, server, "application/json", body); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d want 400 body=%q", body, rr.Code, rr.Body.String())
		}
	}
}

// An empty batch still answers 204, and a valid batch is stored.
func TestServeLogsIngestStoresLogsArray(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	if rr := postLogs(t, server, "application/json", `{"logs":[]}`); rr.Code != http.StatusNoContent {
		t.Fatalf("empty batch: got %d want 204 body=%q", rr.Code, rr.Body.String())
	}
	if rr := postLogs(t, server, "application/json; charset=utf-8", `{"logs":[{"level":"info","message":"shape-ok"}]}`); rr.Code != http.StatusNoContent {
		t.Fatalf("valid batch: got %d want 204 body=%q", rr.Code, rr.Body.String())
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	req = req.WithContext(domain.WithProjectID(req.Context(), 1))
	server.serveLogs(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "shape-ok") {
		t.Fatalf("stored logs: got %d body=%q, want the valid batch's entry", rr.Code, rr.Body.String())
	}
}
