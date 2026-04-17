package bugbarn

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecoverMiddlewareNoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	Init(Options{APIKey: "test-key", Endpoint: srv.URL})
	defer resetGlobal()

	called := false
	handler := RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("expected underlying handler to be called")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestRecoverMiddlewarePanic(t *testing.T) {
	enqueuedCh := make(chan struct{}, 1)

	// Use a test server that signals when it receives an event.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enqueuedCh <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	Init(Options{APIKey: "test-key", Endpoint: srv.URL, QueueSize: 4})
	defer resetGlobal()

	handler := RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	// The middleware re-raises the panic; catch it here.
	panicked := false
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				panicked = true
			}
		}()
		handler.ServeHTTP(rr, req)
	}()

	if !panicked {
		t.Fatal("expected panic to be re-raised")
	}

	// Wait for the event to be delivered to the test server.
	select {
	case <-enqueuedCh:
		// event was delivered
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for panic event to be delivered")
	}
}
