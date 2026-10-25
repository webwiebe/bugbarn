package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
)

func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// A refused dial means the request never left the reader: the writer cannot
// have acted on it, so the SDK may retry safely.
func TestForwardRefusedDialAnswers503WithRetryAfter(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens here any more

	logs := captureDefaultLog(t)
	fwd := NewWriteForwarder("http://" + addr)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/w/check-ins", strings.NewReader(`{"status":"ok"}`))
	rec := httptest.NewRecorder()
	fwd.Forward(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1", got)
	}
	if !strings.Contains(rec.Body.String(), "ingest_unavailable") {
		t.Errorf("body %q, want the ingest_unavailable contract body", rec.Body.String())
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "error_class=dial") {
		t.Errorf("warn log missing the error class:\n%s", out)
	}
}

// A connection that dies after the request went out may already have been
// acted on. Answering a retryable 503 there would let the SDK duplicate an
// ingest event, so it stays 502.
func TestForwardResetAfterTheRequestWasSentStays502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close() // the body was read; no response is ever written
		}
	}))
	defer upstream.Close()

	logs := captureDefaultLog(t)
	fwd := NewWriteForwarder(upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{"message":"x"}`))
	rec := httptest.NewRecorder()
	fwd.Forward(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q on an ambiguous failure, want none", got)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "error_class=eof") {
		t.Errorf("warn log missing the error class:\n%s", out)
	}
}

func TestClassifyForwardError(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	reset := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	cases := []struct {
		name      string
		err       error
		class     string
		neverSent bool
	}{
		{"refused dial", refused, "dial", true},
		{"dns", &net.DNSError{Err: "no such host", Name: "bugbarn-writer"}, "dial", true},
		{"eof", io.EOF, "eof", false},
		{"unexpected eof", io.ErrUnexpectedEOF, "eof", false},
		{"broken pipe", syscall.EPIPE, "reset", false},
		{"reset", reset, "reset", false},
		{"deadline", context.DeadlineExceeded, "timeout", false},
		{"canceled", context.Canceled, "canceled", false},
		{"other", errors.New("boom"), "other", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, neverSent := classifyForwardError(tc.err)
			if class != tc.class || neverSent != tc.neverSent {
				t.Errorf("classify = (%q, %v), want (%q, %v)", class, neverSent, tc.class, tc.neverSent)
			}
		})
	}
}
