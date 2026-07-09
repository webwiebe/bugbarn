package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// TestServeLogsIngestBodyCap verifies the log-ingest endpoint rejects an
// oversized body instead of buffering it into an unbounded slice.
func TestServeLogsIngestBodyCap(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil) // no ingest handler -> 1 MiB default cap

	// ~2 MiB JSON body, well over the default cap.
	big := strings.Repeat("a", 2<<20)
	body := `{"logs":[{"message":"` + big + `"}]}`

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(storage.WithProjectID(req.Context(), 1))

	server.serveLogsIngest(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized log body: got %d want %d body=%q", rr.Code, http.StatusRequestEntityTooLarge, rr.Body.String())
	}
}

// TestServeLogsIngestEntryCap verifies the endpoint rejects a batch with too many
// entries even when the raw body fits under the byte cap.
func TestServeLogsIngestEntryCap(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	var sb strings.Builder
	sb.WriteString(`{"logs":[`)
	for i := 0; i < maxLogEntries+1; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{}`)
	}
	sb.WriteString(`]}`)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", strings.NewReader(sb.String()))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(storage.WithProjectID(req.Context(), 1))

	server.serveLogsIngest(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized log batch: got %d want %d body=%q", rr.Code, http.StatusRequestEntityTooLarge, rr.Body.String())
	}
}
