package bugbarn

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckResponse(t *testing.T) {
	for _, status := range []int{200, 201, 202, 204} {
		if err := checkResponse(status); err != nil {
			t.Errorf("%d: got %v, want nil", status, err)
		}
	}
	for status, want := range map[int]struct {
		reason    string
		retryable bool
	}{
		400: {"malformed_payload", false},
		401: {"unauthorized", false},
		403: {"forbidden", false},
		404: {"status_404", false},
		413: {"payload_too_large", false},
		429: {"spool_full", true},
		500: {"status_500", true},
		503: {"ingest_unavailable", true},
	} {
		var got *responseError
		if !errors.As(checkResponse(status), &got) {
			t.Errorf("%d: no responseError", status)
			continue
		}
		if got.reason() != want.reason || got.retryable() != want.retryable {
			t.Errorf("%d: got %s retryable=%v, want %s retryable=%v", status, got.reason(), got.retryable(), want.reason, want.retryable)
		}
	}
}

func TestRejectedEventIsVisibleAndKeepsTheKeyOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	var out bytes.Buffer
	tr := newTransport("revoked-key", srv.URL, "", 8)
	tr.drops = newDropLog(&out)
	env := envelope{Timestamp: "now", SeverityText: "ERROR"}
	for i := 0; i < 3; i++ {
		if !tr.enqueue(env) {
			t.Fatal("enqueue returned false")
		}
	}
	if !tr.shutdown(2 * time.Second) {
		t.Fatal("shutdown did not drain the queue")
	}

	line := out.String()
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("three rejections of one reason should print one line, got %q", line)
	}
	for _, want := range []string{"1 event dropped", "HTTP 401 unauthorized, permanent", srv.URL} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "revoked-key") {
		t.Errorf("line %q holds the API key", line)
	}
}

func TestUnreachableEventIsVisible(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	endpoint := srv.URL
	srv.Close()

	var out bytes.Buffer
	tr := newTransport("k", endpoint, "", 8)
	tr.drops = newDropLog(&out)
	tr.enqueue(envelope{Timestamp: "now", SeverityText: "ERROR"})
	if !tr.shutdown(10 * time.Second) {
		t.Fatal("shutdown did not drain the queue")
	}

	if line := out.String(); !strings.Contains(line, "unreachable: ") || strings.Count(line, endpoint) != 1 {
		t.Errorf("unexpected line %q", line)
	}
}

func TestRejectedCheckInIsVisible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	var out bytes.Buffer
	Init(Options{APIKey: "k", Endpoint: srv.URL})
	defer Shutdown(2 * time.Second)
	mu.Lock()
	tp.drops = newDropLog(&out)
	mu.Unlock()

	if !CheckIn("sweep", CheckInOK) {
		t.Fatal("CheckIn returned false")
	}
	if !Flush(2 * time.Second) {
		t.Fatal("flush did not wait for the check-in")
	}
	if line := out.String(); !strings.Contains(line, "1 check-in dropped") || !strings.Contains(line, "HTTP 401 unauthorized") {
		t.Errorf("unexpected line %q", line)
	}
}

func TestDropLogLimitsEachReasonAndCountsTheRest(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 10, 31, 15, 0, 0, 0, time.UTC)
	d := newDropLog(&out)
	d.now = func() time.Time { return now }

	unauthorized, unavailable := checkResponse(401), checkResponse(503)
	d.record("event", "http://x", unauthorized)
	d.record("event", "http://x", unauthorized)
	d.record("event", "http://x", unauthorized)
	d.record("event", "http://x", unavailable)
	d.record("check-in", "http://x", unauthorized)
	now = now.Add(dropEvery)
	d.record("event", "http://x", unauthorized)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %q", len(lines), lines)
	}
	for i, want := range []string{
		"1 event dropped since the last line, HTTP 401",
		"1 event dropped since the last line, HTTP 503 ingest_unavailable, retryable",
		"1 check-in dropped since the last line, HTTP 401",
		"3 event dropped since the last line, HTTP 401",
	} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d: %q lacks %q", i, lines[i], want)
		}
	}
}
