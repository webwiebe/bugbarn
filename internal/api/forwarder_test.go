package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteForwarder_Forward(t *testing.T) {
	// Set up a fake upstream writer that echoes back details.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method, path, query.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/events" {
			t.Errorf("expected path /api/v1/events, got %s", r.URL.Path)
		}
		if r.URL.RawQuery != "project=test" {
			t.Errorf("expected query project=test, got %s", r.URL.RawQuery)
		}

		// Verify headers passed through.
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-BugBarn-Key") != "secret123" {
			t.Errorf("expected X-BugBarn-Key secret123, got %s", r.Header.Get("X-BugBarn-Key"))
		}
		if r.Header.Get("Cookie") != "session=abc" {
			t.Errorf("expected Cookie session=abc, got %s", r.Header.Get("Cookie"))
		}

		// Read body.
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"error":"something broke"}` {
			t.Errorf("unexpected body: %s", string(body))
		}

		// Send response with custom headers.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-42")
		w.Header().Set("Set-Cookie", "session=xyz; Path=/")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"evt-1"}`))
	}))
	defer upstream.Close()

	fwd := NewWriteForwarder(upstream.URL)

	body := strings.NewReader(`{"error":"something broke"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events?project=test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BugBarn-Key", "secret123")
	req.Header.Set("Cookie", "session=abc")

	rec := httptest.NewRecorder()
	fwd.Forward(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("expected response Content-Type application/json, got %s", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("X-Request-Id") != "req-42" {
		t.Errorf("expected X-Request-Id req-42, got %s", resp.Header.Get("X-Request-Id"))
	}
	if resp.Header.Get("Set-Cookie") != "session=xyz; Path=/" {
		t.Errorf("expected Set-Cookie to be forwarded, got %s", resp.Header.Get("Set-Cookie"))
	}

	respBody, _ := io.ReadAll(resp.Body)
	if string(respBody) != `{"id":"evt-1"}` {
		t.Errorf("unexpected response body: %s", string(respBody))
	}
}

func TestWriteForwarder_WriterDown(t *testing.T) {
	// Create a server and immediately close it to get a port that refuses connections.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := srv.URL
	srv.Close()

	fwd := NewWriteForwarder(closedURL)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", nil)
	rec := httptest.NewRecorder()
	fwd.Forward(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "upstream writer unavailable") {
		t.Errorf("expected 'upstream writer unavailable' message, got: %s", string(body))
	}
}
