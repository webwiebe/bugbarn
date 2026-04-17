package bugbarn

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func resetGlobal() {
	mu.Lock()
	if tp != nil {
		tp.shutdown(2 * time.Second)
		tp = nil
	}
	opts = Options{}
	mu.Unlock()
}

func TestCaptureErrorBeforeInit(t *testing.T) {
	resetGlobal()
	if CaptureError(errors.New("test")) {
		t.Fatal("expected false when not initialised")
	}
}

func TestCaptureErrorAfterInit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	Init(Options{APIKey: "test-key", Endpoint: srv.URL})
	defer resetGlobal()

	if !CaptureError(errors.New("something broke")) {
		t.Fatal("expected true after init")
	}
}

func TestCaptureMessageAfterInit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	Init(Options{APIKey: "test-key", Endpoint: srv.URL})
	defer resetGlobal()

	if !CaptureMessage("hello world") {
		t.Fatal("expected true after init")
	}
}

func TestFlushNoopWhenNotInited(t *testing.T) {
	resetGlobal()
	// Should return true (nothing to drain) and not panic.
	if !Flush(100 * time.Millisecond) {
		t.Fatal("expected true when not initialised")
	}
}

func TestShutdownDrainsQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	Init(Options{APIKey: "test-key", Endpoint: srv.URL, QueueSize: 16})

	for i := 0; i < 5; i++ {
		CaptureError(errors.New("drain test"))
	}

	// Shutdown must not panic and should return within timeout.
	Shutdown(2 * time.Second)
}
