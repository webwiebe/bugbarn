package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunRequiresEndpointAndKey(t *testing.T) {
	var out bytes.Buffer
	err := run(func(string) string { return "" }, &out)
	if err == nil || !strings.Contains(err.Error(), "BUGBARN_ENDPOINT") {
		t.Fatalf("run = %v, want a message naming BUGBARN_ENDPOINT", err)
	}
	if out.Len() != 0 {
		t.Fatalf("output before configuration = %q", out.String())
	}
}

func TestExitCode(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Fatalf("exitCode(nil) = %d, want 0", got)
	}
	if got := exitCode(errors.New("unset")); got != 1 {
		t.Fatalf("exitCode(err) = %d, want 1", got)
	}
}

func TestRunCapturesAnErrorAndFlushesItToTheEndpoint(t *testing.T) {
	received := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("X-BugBarn-Api-Key")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	env := map[string]string{"BUGBARN_ENDPOINT": srv.URL, "BUGBARN_API_KEY": "sample-key"}
	var out bytes.Buffer
	if err := run(func(k string) string { return env[k] }, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Captured error") || !strings.Contains(out.String(), "flushing") {
		t.Fatalf("output = %q", out.String())
	}
	select {
	case key := <-received:
		if key != "sample-key" {
			t.Fatalf("endpoint received api key %q", key)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the captured error never reached the endpoint")
	}
}
