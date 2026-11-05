package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fakeServer(t *testing.T, finalStatus, finalBody string, finalCode int) *httptest.Server {
	t.Helper()
	var polls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/setup/geo/key-requests":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"req1","pollSecret":"sec","status":"pending","expiresAt":"2026-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/setup/geo/key-requests/req1":
			if r.Header.Get("X-BugBarn-Poll-Secret") != "sec" {
				http.Error(w, "nope", http.StatusNotFound)
				return
			}
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"status":"pending"}`))
				return
			}
			w.WriteHeader(finalCode)
			_, _ = w.Write([]byte(finalBody))
		default:
			http.NotFound(w, r)
		}
	}))
}

func run(t *testing.T, srv *httptest.Server) (stdout, stderr string, err error) {
	t.Helper()
	var out, info bytes.Buffer
	err = runKeyRequest(keyRequestRun{
		base: srv.URL, slug: "geo", label: "org/repo prod",
		out: &out, info: &info,
		interval: time.Millisecond, timeout: 5 * time.Second,
		http: srv.Client(),
	})
	return out.String(), info.String(), err
}

func TestKeyRequestWaitsAndPrintsKeyOnlyToStdout(t *testing.T) {
	srv := fakeServer(t, "approved", `{"status":"approved","apiKey":"abc123"}`, http.StatusOK)
	defer srv.Close()
	stdout, stderr, err := run(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != "abc123" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stderr, "abc123") {
		t.Fatalf("stderr leaks a secret: %q", stderr)
	}
}

func TestKeyRequestDeniedAndExpiredPrintNothing(t *testing.T) {
	for _, tc := range []struct {
		body string
		code int
		want string
	}{
		{`{"status":"denied"}`, http.StatusOK, "denied"},
		{`{"status":"expired"}`, http.StatusGone, "expired"},
	} {
		srv := fakeServer(t, "", tc.body, tc.code)
		stdout, _, err := run(t, srv)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v", tc.want, err)
		}
		if stdout != "" {
			t.Fatalf("%s: stdout = %q", tc.want, stdout)
		}
	}
}

func TestKeyRequestRequiresOutputSecret(t *testing.T) {
	err := cmdKeyRequest([]string{"geo", "--label", "x", "--url", "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "--output-secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestReorderFlagsAcceptsSlugAnywhere(t *testing.T) {
	got := strings.Join(reorderFlags([]string{"geo", "--label", "a b", "--output-secret"}), "|")
	if got != "--label|a b|--output-secret|geo" {
		t.Fatalf("got %q", got)
	}
}

// A poll that fails on the way is retried: the poll secret exists only in the
// running command, so giving up strands the request (bugbarn#256).
func TestKeyRequestRetriesTransientPollFailures(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A fresh connection per request: net/http silently retries a GET once
		// when a reused keep-alive connection closes, which would hide a reset.
		w.Header().Set("Connection", "close")
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"req1","pollSecret":"sec-xyz","status":"pending","expiresAt":"2099-01-01T00:00:00Z"}`))
		case r.Header.Get("X-BugBarn-Poll-Secret") != "sec-xyz":
			http.Error(w, "nope", http.StatusNotFound)
		default:
			switch polls.Add(1) {
			case 1, 2:
				// Close the connection without an answer, as the proxy did.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				_ = conn.Close()
			case 3:
				http.Error(w, "bad gateway", http.StatusBadGateway)
			case 4:
				_, _ = w.Write([]byte(`{"status":"pending"}`))
			default:
				_, _ = w.Write([]byte(`{"status":"approved","apiKey":"abc123"}`))
			}
		}
	}))
	defer srv.Close()

	stdout, stderr, err := run(t, srv)
	if err != nil {
		t.Fatalf("err = %v\nstderr:\n%s", err, stderr)
	}
	if stdout != "abc123\n" {
		t.Fatalf("stdout = %q, want the key once", stdout)
	}
	if n := strings.Count(stderr, "Poll failed, retrying"); n != 3 {
		t.Fatalf("retry lines = %d, want 3:\n%s", n, stderr)
	}
	if strings.Contains(stderr, "sec-xyz") || strings.Contains(stderr, "abc123") {
		t.Fatalf("stderr leaks a secret:\n%s", stderr)
	}
}

// A 4xx other than 410 does not change on retry, so it still ends the command.
func TestKeyRequestStopsOnAClientError(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"req1","pollSecret":"sec","status":"pending","expiresAt":"2099-01-01T00:00:00Z"}`))
			return
		}
		polls.Add(1)
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := run(t, srv)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v, want HTTP 404", err)
	}
	if n := polls.Load(); n != 1 {
		t.Fatalf("polls = %d, want 1", n)
	}
}
