package writeforward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFlushRetriesUntilTheWriterAccepts(t *testing.T) {
	var calls atomic.Int32
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The first request fails as a restarting writer would.
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer writer.Close()
	sf := newTestSpool(t, writer.URL)
	spoolRecord(t, sf)
	spoolRecord(t, sf)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sf.Flush(ctx, time.Millisecond); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if sf.Pending() != 0 {
		t.Fatalf("pending = %d after Flush, want 0", sf.Pending())
	}
	if calls.Load() < 3 {
		t.Fatalf("writer saw %d requests, want the failed one retried", calls.Load())
	}
}

func TestFlushReturnsTheLastErrorWhenTheDeadlinePasses(t *testing.T) {
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer writer.Close()
	sf := newTestSpool(t, writer.URL)
	spoolRecord(t, sf)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := sf.Flush(ctx, time.Millisecond); err == nil {
		t.Fatal("Flush returned nil with the writer down")
	}
	if sf.Pending() != 1 {
		t.Fatalf("pending = %d, want the record kept on disk", sf.Pending())
	}
}

func TestFlushOnAnEmptySpoolReturnsAtOnce(t *testing.T) {
	sf := newTestSpool(t, "http://127.0.0.1:1")
	if err := sf.Flush(context.Background(), time.Hour); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}
