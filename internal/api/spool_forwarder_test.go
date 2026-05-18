package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestSpool(t *testing.T, writerURL string) *SpoolForwarder {
	t.Helper()
	sf, err := NewSpoolForwarder(t.TempDir(), writerURL, 1<<20, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sf.Close() })
	return sf
}

func spoolRecord(t *testing.T, sf *SpoolForwarder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("Forward returned %d, want 202", rec.Code)
	}
}

func TestSpoolForwarder_DrainOnce_EmptySpool(t *testing.T) {
	t.Parallel()

	sf := newTestSpool(t, "http://localhost:0")
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce on empty spool: %v", err)
	}
}

func TestSpoolForwarder_DrainOnce_ForwardsRecords(t *testing.T) {
	t.Parallel()

	var received atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	sf := newTestSpool(t, upstream.URL)
	spoolRecord(t, sf)
	spoolRecord(t, sf)

	if sf.Pending() != 2 {
		t.Fatalf("expected 2 pending, got %d", sf.Pending())
	}

	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	if got := received.Load(); got != 2 {
		t.Fatalf("expected 2 requests forwarded, got %d", got)
	}
	if sf.Pending() != 0 {
		t.Fatalf("expected 0 pending after drain, got %d", sf.Pending())
	}
}

func TestSpoolForwarder_DrainOnce_ReturnsErrorWhenWriterDown(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := srv.URL
	srv.Close()

	sf := newTestSpool(t, closedURL)
	spoolRecord(t, sf)
	pendingBefore := sf.Pending()

	err := sf.DrainOnce(context.Background())
	if err == nil {
		t.Fatal("expected error when writer is down, got nil")
	}
	// Cursor must not have advanced — the record remains replayable.
	if sf.Pending() != pendingBefore {
		t.Fatalf("pending changed from %d to %d after failed drain; cursor should not advance on error", pendingBefore, sf.Pending())
	}
}

func TestSpoolForwarder_DrainOnce_RespectsContextDeadline(t *testing.T) {
	t.Parallel()

	unblock := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
	}))
	defer upstream.Close()
	defer close(unblock)

	sf := newTestSpool(t, upstream.URL)
	sf.client = &http.Client{Timeout: 5 * time.Second}
	spoolRecord(t, sf)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := sf.DrainOnce(ctx); err == nil {
		t.Fatal("expected error when context deadline exceeded, got nil")
	}
}
