package selflog

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLogsURL(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"https://bb.example":                 "https://bb.example/api/v1/logs",
		"https://bb.example/":                "https://bb.example/api/v1/logs",
		"https://bb.example/api/v1/events":   "https://bb.example/api/v1/logs",
		"http://bugbarn:8080/api/v1/events/": "http://bugbarn:8080/api/v1/logs",
		"http://bugbarn.ns.svc:8080":         "http://bugbarn.ns.svc:8080/api/v1/logs",
	} {
		if got := LogsURL(in); got != want {
			t.Errorf("LogsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

type received struct {
	path, key, project, contentType string
	body                            map[string]any
}

func newShipperServer(t *testing.T, status int) (*LogShipper, chan received) {
	t.Helper()
	got := make(chan received, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		got <- received{r.URL.Path, r.Header.Get("X-BugBarn-Api-Key"), r.Header.Get("X-BugBarn-Project"),
			r.Header.Get("Content-Type"), body}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return NewLogShipper(srv.URL, "self-key", "bugbarn-service"), got
}

func TestShipPostsOneLineWithTheSelfKey(t *testing.T) {
	t.Parallel()
	s, got := newShipperServer(t, http.StatusAccepted)
	fixed := time.Date(2026, 11, 12, 22, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }

	s.Ship("info", "bound api key named another project", map[string]any{"key_project": "a", "header_project": "b"})

	select {
	case r := <-got:
		if r.path != "/api/v1/logs" || r.key != "self-key" || r.project != "bugbarn-service" || r.contentType != "application/json" {
			t.Fatalf("request = %+v", r)
		}
		logs, _ := r.body["logs"].([]any)
		if len(logs) != 1 {
			t.Fatalf("logs = %v", r.body["logs"])
		}
		line, _ := logs[0].(map[string]any)
		if line["msg"] != "bound api key named another project" || line["level"] != "info" ||
			line["key_project"] != "a" || line["header_project"] != "b" || line["time"] != float64(fixed.UnixMilli()) {
			t.Fatalf("line = %v", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no request reached the server")
	}
}

func TestShipDoesNotMutateTheCallersAttrs(t *testing.T) {
	t.Parallel()
	s, got := newShipperServer(t, http.StatusAccepted)
	attrs := map[string]any{"k": "v"}
	s.Ship("info", "m", attrs)
	<-got
	if len(attrs) != 1 {
		t.Fatalf("attrs = %v, want the caller's map untouched", attrs)
	}
}

func TestShipDropsWhenAllSlotsAreBusy(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		<-release
	}))
	defer srv.Close()
	s := NewLogShipper(srv.URL, "k", "p")
	finished := make(chan struct{}, 16)
	s.done = func(error) { finished <- struct{}{} }

	start := time.Now()
	for i := 0; i < logShipSlots+6; i++ {
		s.Ship("info", "m", nil)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Ship blocked on a full shipper")
	}
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := hits
		mu.Unlock()
		if n == logShipSlots {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("server saw %d requests, want %d in flight", n, logShipSlots)
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(release)
	for i := 0; i < logShipSlots; i++ {
		<-finished
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != logShipSlots {
		t.Fatalf("server saw %d requests, want %d: the extra lines must be dropped", hits, logShipSlots)
	}
}

func TestShipReportsAFailedSendToTheHookOnly(t *testing.T) {
	t.Parallel()
	s, got := newShipperServer(t, http.StatusForbidden)
	errs := make(chan error, 1)
	s.done = func(err error) { errs <- err }
	s.Ship("info", "m", nil)
	<-got
	if err := <-errs; err == nil {
		t.Fatal("a 403 did not surface as an error to the hook")
	}
}
