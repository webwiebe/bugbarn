package oomwatch

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 7, 6, 0, 0, 0, time.UTC)

func oomPod(ns, name, uid, container string, finished time.Time, inLastState bool) pod {
	var p pod
	p.Metadata.Namespace, p.Metadata.Name, p.Metadata.UID = ns, name, uid
	spec := containerSpec{Name: container}
	spec.Resources.Limits = map[string]string{"memory": "256Mi"}
	p.Spec.Containers = []containerSpec{spec}
	st := containerStatus{Name: container, RestartCount: 3}
	term := &terminated{Reason: "OOMKilled", ExitCode: 137, FinishedAt: finished}
	if inLastState {
		st.LastState.Terminated = term
	} else {
		st.State.Terminated = term
	}
	p.Status.ContainerStatuses = []containerStatus{st}
	return p
}

func TestKillsReadsStateAndLastStateAndIgnoresOtherReasons(t *testing.T) {
	other := oomPod("a", "p3", "u3", "app", t0, true)
	other.Status.ContainerStatuses[0].LastState.Terminated.Reason = "Error"
	got := kills([]pod{
		oomPod("a", "p1", "u1", "app", t0, true),
		oomPod("a", "p2", "u2", "app", t0, false),
		other,
	})
	if len(got) != 2 {
		t.Fatalf("kills = %d, want 2: %+v", len(got), got)
	}
	if got[0].MemoryLimit != "256Mi" || got[0].RestartCount != 3 || got[0].ExitCode != 137 {
		t.Fatalf("details lost: %+v", got[0])
	}
}

func TestTrackerReportsOnceSkipsHistoryAndRetriesFailures(t *testing.T) {
	tr := newTracker(t0, 10*time.Minute)
	old := kills([]pod{oomPod("a", "old", "u0", "app", t0.Add(-time.Hour), true)})
	if got := tr.fresh(old); len(got) != 0 {
		t.Fatalf("history before the lookback was reported: %+v", got)
	}
	recent := kills([]pod{oomPod("a", "p", "u1", "app", t0.Add(-2*time.Minute), true)})
	first := tr.fresh(recent)
	if len(first) != 1 {
		t.Fatalf("first scan = %d, want 1", len(first))
	}
	if again := tr.fresh(recent); len(again) != 0 {
		t.Fatalf("the same kill was reported twice")
	}
	tr.unmark(first[0])
	if retry := tr.fresh(recent); len(retry) != 1 {
		t.Fatalf("a failed report was not retried")
	}
	tr.fresh(nil)
	if len(tr.seen) != 0 {
		t.Fatalf("seen map kept entries for pods that left the list: %d", len(tr.seen))
	}
}

type ingestLog struct {
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	project, key string
	body         map[string]any
}

func fakeIngest(t *testing.T, status int) (*httptest.Server, *ingestLog) {
	t.Helper()
	log := &ingestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		log.mu.Lock()
		log.reqs = append(log.reqs, recorded{r.Header.Get("X-BugBarn-Project"), r.Header.Get("X-BugBarn-Api-Key"), body})
		log.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func fakeKube(t *testing.T, pages [][]pod) *kubeClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		idx := 0
		if c := r.URL.Query().Get("continue"); c != "" {
			idx = int(c[0] - '0')
		}
		list := map[string]any{"items": pages[idx], "metadata": map[string]any{}}
		if idx+1 < len(pages) {
			list["metadata"] = map[string]any{"continue": string(rune('0' + idx + 1))}
		}
		_ = json.NewEncoder(w).Encode(list)
	}))
	t.Cleanup(srv.Close)
	return &kubeClient{base: srv.URL, token: func() (string, error) { return "tok", nil }, http: srv.Client()}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestScanReportsEachKillToTheNamespaceProjectAndGroupsRepeats(t *testing.T) {
	ingest, log := fakeIngest(t, http.StatusAccepted)
	kube := fakeKube(t, [][]pod{
		{oomPod("brandtrace-production", "web-1", "u1", "web", t0.Add(-time.Minute), true)},
		{oomPod("brandtrace-production", "web-2", "u2", "web", t0.Add(-30*time.Second), false), oomPod("misc", "job", "u3", "job", t0, true)},
	})
	rep := &reporter{
		endpoint: ingest.URL, apiKey: "secret", cluster: "layer7", defaultProject: "cluster",
		projects: map[string]string{"brandtrace-production": "brandtrace"}, http: ingest.Client(),
	}
	tr := newTracker(t0, 10*time.Minute)
	scan(context.Background(), quiet(), kube, rep, tr)
	scan(context.Background(), quiet(), kube, rep, tr)

	if len(log.reqs) != 3 {
		t.Fatalf("events = %d, want 3 (one per kill, none repeated by the second scan)", len(log.reqs))
	}
	byPod := map[string]recorded{}
	for _, r := range log.reqs {
		byPod[r.body["attributes"].(map[string]any)["k8s.pod.name"].(string)] = r
	}
	if byPod["web-1"].project != "brandtrace" || byPod["job"].project != "cluster" {
		t.Fatalf("projects = %q and %q", byPod["web-1"].project, byPod["job"].project)
	}
	if byPod["web-1"].key != "secret" {
		t.Fatalf("api key header = %q", byPod["web-1"].key)
	}
	f1, f2 := byPod["web-1"].body["fingerprint"], byPod["web-2"].body["fingerprint"]
	if f1 != "oomkilled:brandtrace-production/web" || f1 != f2 {
		t.Fatalf("fingerprints %v and %v should be one group", f1, f2)
	}
	attrs := byPod["web-1"].body["attributes"].(map[string]any)
	if attrs["k8s.memory.limit"] != "256Mi" || attrs["k8s.cluster.name"] != "layer7" || attrs["k8s.restart.count"] != float64(3) {
		t.Fatalf("attributes = %+v", attrs)
	}
	if byPod["web-1"].body["exception"].(map[string]any)["type"] != "OOMKilled" {
		t.Fatalf("exception type = %v", byPod["web-1"].body["exception"])
	}
}

func TestScanRetriesAReportThatFailed(t *testing.T) {
	ingest, log := fakeIngest(t, http.StatusServiceUnavailable)
	kube := fakeKube(t, [][]pod{{oomPod("a", "p", "u", "c", t0, true)}})
	rep := &reporter{endpoint: ingest.URL, apiKey: "k", defaultProject: "cluster", http: ingest.Client()}
	tr := newTracker(t0, time.Minute)
	scan(context.Background(), quiet(), kube, rep, tr)
	scan(context.Background(), quiet(), kube, rep, tr)
	if len(log.reqs) != 2 {
		t.Fatalf("attempts = %d, want 2 (the failed kill is tried again)", len(log.reqs))
	}
}

func TestListPodsFollowsContinueAndSurfacesAPIErrors(t *testing.T) {
	kube := fakeKube(t, [][]pod{{oomPod("a", "p1", "u1", "c", t0, true)}, {oomPod("a", "p2", "u2", "c", t0, true)}})
	pods, err := kube.listPods(context.Background())
	if err != nil || len(pods) != 2 {
		t.Fatalf("pods = %d err = %v, want 2 pods across 2 pages", len(pods), err)
	}
	kube.token = func() (string, error) { return "wrong", nil }
	if _, err := kube.listPods(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want HTTP 403", err)
	}
}

func TestLoadConfig(t *testing.T) {
	env := map[string]string{"OOMWATCH_ENDPOINT": "http://x/api/v1/events", "OOMWATCH_API_KEY": "k",
		"OOMWATCH_PROJECTS": "a=one, b = two ,bad,=x", "OOMWATCH_INTERVAL": "45s"}
	cfg, err := loadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.defaultProject != "cluster" || cfg.interval != 45*time.Second || cfg.projects["a"] != "one" || cfg.projects["b"] != "two" || len(cfg.projects) != 2 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("missing endpoint and key were accepted")
	}
	env["OOMWATCH_INTERVAL"] = "0s"
	if _, err := loadConfig(func(k string) string { return env[k] }); err == nil {
		t.Fatal("a zero interval was accepted")
	}
}
