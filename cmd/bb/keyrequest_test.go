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
