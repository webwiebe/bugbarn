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

func TestSpoolForwarder_RejectsMalformedEvent(t *testing.T) {
	t.Parallel()

	sf := newTestSpool(t, "http://localhost:0")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Forward status = %d, want 400", rec.Code)
	}
	// A rejected event must not be spooled for forwarding.
	if sf.Pending() != 0 {
		t.Fatalf("expected 0 pending after reject, got %d", sf.Pending())
	}
}

// A JSON log body without a "logs" array is refused before it is spooled: the
// writer would store nothing from it, and the sender would never learn (#263).
func TestSpoolForwarder_RejectsLogBodyWithoutLogsArray(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`not json`, `{}`, `{"logs":null}`, `{"logs":"x"}`, `{"logs":{}}`, `[{"message":"bare"}]`} {
		sf := newTestSpool(t, "http://localhost:0")
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		sf.Forward(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: Forward status = %d, want 400", body, rec.Code)
		}
		if sf.Pending() != 0 {
			t.Errorf("%s: %d pending, want 0: a refused log body must not be spooled", body, sf.Pending())
		}
	}
}

// Valid JSON log bodies, an empty batch included, and NDJSON bodies are
// spooled. NDJSON lines are parsed by the writer, which skips a bad line.
func TestSpoolForwarder_AcceptsLogBodies(t *testing.T) {
	t.Parallel()

	cases := []struct{ contentType, body string }{
		{"application/json", `{"logs":[{"message":"ok"}]}`},
		{"application/json; charset=utf-8", `{"logs":[]}`},
		{"application/x-ndjson", "{\"message\":\"ok\"}\nnot json\n"},
	}
	for _, c := range cases {
		sf := newTestSpool(t, "http://localhost:0")
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", strings.NewReader(c.body))
		req.Header.Set("Content-Type", c.contentType)
		rec := httptest.NewRecorder()
		sf.Forward(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Errorf("%s %q: Forward status = %d, want 202", c.contentType, c.body, rec.Code)
		}
		if sf.Pending() == 0 {
			t.Errorf("%s %q: nothing pending, want the body spooled", c.contentType, c.body)
		}
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

	sf := newTestSpool(t, deadWriterURL(t))
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

// Drain forwards what the spool holds, advances the persisted cursor over the
// records the writer took, and returns when its context ends.
func TestSpoolForwarder_Drain_ForwardsAdvancesCursorAndStops(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sf.Drain(ctx); close(done) }()

	deadline := time.Now().Add(30 * time.Second)
	for sf.Pending() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("spool still has %d pending after 30s", sf.Pending())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Drain did not return after its context was canceled")
	}

	if got := received.Load(); got != 2 {
		t.Errorf("forwarded %d requests, want 2", got)
	}
	// The cursor sits at the end of the spool, so a restart replays nothing.
	offset, err := readCursor(sf.dir)
	if err != nil {
		t.Fatalf("readCursor: %v", err)
	}
	if records, _, err := readRecords(sf.path, offset); err != nil || len(records) != 0 {
		t.Errorf("after drain: %d records past the cursor, err %v; want none", len(records), err)
	}
}

// A writer that refuses a record keeps it in the spool: the cursor stays put and
// the record stays pending for the next pass.
func TestSpoolForwarder_Drain_KeepsRecordsTheWriterRefuses(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	sf := newTestSpool(t, upstream.URL)
	spoolRecord(t, sf)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sf.Drain(ctx); close(done) }()

	deadline := time.Now().Add(30 * time.Second)
	for attempts.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("writer was never asked to take the record")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if sf.Pending() != 1 {
		t.Errorf("pending = %d, want 1 (the refused record)", sf.Pending())
	}
	offset, err := readCursor(sf.dir)
	if err != nil {
		t.Fatalf("readCursor: %v", err)
	}
	if records, _, err := readRecords(sf.path, offset); err != nil || len(records) != 1 {
		t.Errorf("records past the cursor = %d, err %v; want 1", len(records), err)
	}
}
