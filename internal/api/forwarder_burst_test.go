package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// A writer closes keep-alive connections it considers idle. The forwarder's
// next request can land on such a connection and fail before any response
// byte. A request with a streaming body cannot be replayed by the transport,
// so that failure used to surface as a 502 for a request that never reached
// the handler.
func TestForwardSurvivesAWriterThatDropsAReusedConnection(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reused := seen[r.RemoteAddr]
		seen[r.RemoteAddr] = true
		mu.Unlock()
		if reused {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()

	fwd := NewWriteForwarder(upstream.URL)
	for i := range 6 {
		// A bare reader hides the length and the rewind, like a real inbound body.
		body := struct{ *strings.Reader }{strings.NewReader(`{"status":"ok"}`)}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/w/check-ins", body)
		rec := httptest.NewRecorder()
		fwd.Forward(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("request %d: status %d, want 201 (a reused connection dropped by the writer must be retried)", i, rec.Code)
		}
	}
}
