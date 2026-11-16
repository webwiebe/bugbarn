package oomwatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func offCluster(t *testing.T) {
	t.Helper()
	for _, p := range []string{tokenPath, caPath} {
		if _, err := os.Stat(p); err == nil {
			t.Skip("running inside a cluster: service account files exist")
		}
	}
}

func TestNewInClusterClientUsesTheGivenBaseOrNeedsTheServiceAccount(t *testing.T) {
	c, err := newInClusterClient("http://kube.example:8001/")
	if err != nil || c.base != "http://kube.example:8001" {
		t.Fatalf("client = %+v err = %v, want the base without its trailing slash", c, err)
	}
	offCluster(t)
	if _, err := newInClusterClient(""); err == nil || !strings.Contains(err.Error(), "cluster CA") {
		t.Fatalf("err = %v, want the missing cluster CA reported", err)
	}
}

func TestReadTokenReportsAMissingServiceAccountToken(t *testing.T) {
	offCluster(t)
	if _, err := readToken(); err == nil || !strings.Contains(err.Error(), "service account token") {
		t.Fatalf("err = %v, want the missing token reported", err)
	}
}

func TestGetPageSurfacesTokenAndDecodeFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	t.Cleanup(srv.Close)
	c := &kubeClient{base: srv.URL, token: func() (string, error) { return "tok", nil }, http: srv.Client()}
	if _, err := c.listPods(context.Background()); err == nil || !strings.Contains(err.Error(), "decode pod list") {
		t.Fatalf("err = %v, want a decode failure", err)
	}
	c.token = func() (string, error) { return "", os.ErrNotExist }
	if _, err := c.listPods(context.Background()); err == nil {
		t.Fatal("a token failure was swallowed")
	}
	c.token = func() (string, error) { return "tok", nil }
	c.base = "http://[::1"
	if _, err := c.listPods(context.Background()); err == nil {
		t.Fatal("a malformed API base was accepted")
	}
}

func TestPayloadNamesAMissingMemoryLimit(t *testing.T) {
	r := &reporter{cluster: "layer7", defaultProject: "cluster"}
	body := r.payload(kill{Namespace: "ns", Container: "c", Pod: "p", FinishedAt: t0})
	msg := body["exception"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "none set") {
		t.Fatalf("message = %q, want the missing limit named", msg)
	}
}

func TestReportRejectsAMalformedEndpointAndAnUnreachableOne(t *testing.T) {
	r := &reporter{endpoint: "http://bad\x7fhost", defaultProject: "cluster", http: http.DefaultClient}
	if err := r.report(context.Background(), kill{Namespace: "ns"}); err == nil {
		t.Fatal("a malformed endpoint was accepted")
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	r = &reporter{endpoint: url, defaultProject: "cluster", http: &http.Client{Timeout: time.Second}}
	if err := r.report(context.Background(), kill{Namespace: "ns"}); err == nil || !strings.Contains(err.Error(), "send event") {
		t.Fatalf("err = %v, want the send failure", err)
	}
}

func TestScanLogsAListFailureAndKeepsGoing(t *testing.T) {
	kube := fakeKube(t, [][]pod{{}})
	kube.token = func() (string, error) { return "wrong", nil }
	scan(context.Background(), quiet(), kube, &reporter{}, newTracker(t0, time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scan(ctx, quiet(), kube, &reporter{}, newTracker(t0, time.Minute))
}

func TestLoopScansOnceAndStopsWhenTheContextEnds(t *testing.T) {
	ingest, log := fakeIngest(t, http.StatusAccepted)
	kube := fakeKube(t, [][]pod{{oomPod("a", "p", "u", "c", time.Now(), true)}})
	rep := &reporter{endpoint: ingest.URL, apiKey: "k", defaultProject: "cluster", http: ingest.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop(ctx, quiet(), kube, rep, time.Hour, newTracker(time.Now(), time.Minute)) }()
	deadline := time.After(10 * time.Second)
	for {
		log.mu.Lock()
		n := len(log.reqs)
		log.mu.Unlock()
		if n == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the first scan never reported the kill")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop = %v, want nil on cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("loop did not stop after the context ended")
	}
}

func TestRunRejectsABadConfigAndStopsOnCancel(t *testing.T) {
	t.Setenv("OOMWATCH_ENDPOINT", "")
	t.Setenv("OOMWATCH_API_KEY", "")
	if err := Run(context.Background(), quiet()); err == nil {
		t.Fatal("Run started without an endpoint and key")
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	t.Setenv("OOMWATCH_ENDPOINT", srv.URL)
	t.Setenv("OOMWATCH_API_KEY", "k")
	t.Setenv("OOMWATCH_KUBE_API", srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, quiet()); err != nil {
		t.Fatalf("Run = %v, want nil once the context ended", err)
	}
}
