package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func TestServerSettersRecordTheirConfiguration(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	s := NewServer(nil, store, nil)

	s.SetAuthEnvironment("  production ")
	if s.environment != "production" {
		t.Errorf("environment = %q, want trimmed value", s.environment)
	}
	s.SetMaxSourceMapBytes(0)
	before := s.maxSourceMapBytes
	s.SetMaxSourceMapBytes(1234)
	if s.maxSourceMapBytes != 1234 || before == 1234 {
		t.Errorf("maxSourceMapBytes = %d (was %d), want 1234 after a positive value", s.maxSourceMapBytes, before)
	}
	s.SetMaxSourceMapBytes(-5)
	if s.maxSourceMapBytes != 1234 {
		t.Errorf("a non-positive size must keep the previous limit, got %d", s.maxSourceMapBytes)
	}
	s.SetFunnelBarnConfig("https://funnel.example", "fk")
	s.SetSelfReportingConfig("selfkey", "bugbarn-web")
	s.SetAutoApproveProjects(true)
	s.SetRetentionDays(30)
	s.SetDBPath("/tmp/x.db")
	s.SetOIDCRefreshGrace(0)
	s.SetOIDCRefreshGrace(2 * time.Hour)
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	s.SetTrustedProxies([]*net.IPNet{cidr})
	if s.funnelBarnEndpoint != "https://funnel.example" || s.funnelBarnAPIKey != "fk" ||
		s.selfAPIKey != "selfkey" || s.selfProject != "bugbarn-web" ||
		!s.autoApproveProjects || s.globalRetentionDays != 30 || s.dbPath != "/tmp/x.db" ||
		s.oidcRefreshGrace != 2*time.Hour || len(s.trustedProxies) != 1 {
		t.Errorf("setters did not record their values: %+v", s)
	}

	s.SetInternalSessionSecret("   ")
	if len(s.internalSessionSecret) != 0 {
		t.Error("a blank secret must leave the internal endpoints disabled")
	}
	s.SetInternalSessionSecret(" shh ")
	if string(s.internalSessionSecret) != "shh" {
		t.Errorf("secret = %q, want trimmed", s.internalSessionSecret)
	}
}

func TestRuntimeConfigReflectsFunnelBarnAndSelfReporting(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	s := NewServer(nil, store, nil)
	s.SetFunnelBarnConfig("https://funnel.example", "fk")
	s.SetSelfReportingConfig("selfkey", "bugbarn-web")

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/runtime-config", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("runtime-config = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"https://funnel.example", "bugbarn-web"} {
		if !strings.Contains(body, want) {
			t.Errorf("runtime-config %q lacks %q", body, want)
		}
	}
}

func TestStartRunsLimiterCleanupUntilContextEnds(t *testing.T) {
	t.Parallel()
	s := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)

	done := make(chan struct{})
	go func() {
		s.cleanupLoginLimiter(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanupLoginLimiter did not return after its context ended")
	}
}

func TestWorkerHealthReportIsAbsentWithoutStatus(t *testing.T) {
	t.Parallel()
	wr, level := (&Server{}).workerHealthReport()
	if wr != nil || level != "" {
		t.Errorf("report = %+v level %q, want none", wr, level)
	}
}

func TestWorkerHealthReportCarriesStatusSnapshot(t *testing.T) {
	t.Parallel()
	status := worker.NewStatus()
	status.RecordProcessed(7)
	status.RecordDeadLetter()
	status.SetPendingRecords(3)
	s := &Server{}
	s.SetWorkerStatus(status)

	wr, level := s.workerHealthReport()
	if wr == nil {
		t.Fatal("expected a worker report")
	}
	if level != "ok" || !wr.Healthy {
		t.Errorf("fresh worker = level %q healthy %v, want ok/true", level, wr.Healthy)
	}
	if wr.ProcessedTotal != 7 || wr.DeadLetterCount != 1 || wr.PendingRecords != 3 {
		t.Errorf("counters = %+v", wr)
	}
	if wr.LastAdvance == nil {
		t.Error("lastAdvance missing")
	}
	if wr.StaleSince != nil {
		t.Errorf("a fresh worker has no staleSince, got %q", *wr.StaleSince)
	}
}

func TestDetailedHealthIncludesWorkerReport(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	s := NewServer(nil, store, nil)
	status := worker.NewStatus()
	status.SetPendingRecords(2)
	s.SetWorkerStatus(status)

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/health?detail=true", nil))
	var body struct {
		Worker *struct {
			Level          string `json:"level"`
			PendingRecords int64  `json:"pendingRecords"`
		} `json:"worker"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("health body %q: %v", rr.Body.String(), err)
	}
	if body.Worker == nil || body.Worker.PendingRecords != 2 {
		t.Errorf("worker section = %+v in %q", body.Worker, rr.Body.String())
	}
}

func TestApplyGroupScopeResolvesMembers(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	ctx := context.Background()
	s := NewServer(nil, store, nil)

	home, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGroup(ctx, "Team", "team"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignProjectToGroup(ctx, "default", "team"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGroup(ctx, "Empty", "empty"); err != nil {
		t.Fatal(err)
	}

	scoped := func(header string) ([]int64, bool) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("X-BugBarn-Group", header)
		}
		return domain.ProjectIDsFromContext(s.applyGroupScope(req).Context())
	}

	ids, ok := scoped("team")
	if !ok || len(ids) != 1 || ids[0] != home.ID {
		t.Errorf("group team = %v ok=%v, want [%d]", ids, ok, home.ID)
	}
	for name, header := range map[string]string{"no header": "", "unknown group": "nope", "empty group": "empty"} {
		if ids, ok := scoped(header); ok {
			t.Errorf("%s: scope = %v, want none", name, ids)
		}
	}
}

func TestIngestNDJSONLogs(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	body := "{\"message\":\"one\",\"level\":30}\n\nnot json\n{\"message\":\"two\",\"level\":30}\n"
	rr := postLogs(t, server, "application/x-ndjson; charset=utf-8", body)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ndjson ingest = %d %q, want 204", rr.Code, rr.Body.String())
	}
	got, err := store.QueryLogEntries(context.Background(), storage.LogQuery{ProjectID: 1}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("stored %d entries, want 2 (blank and malformed lines skipped)", len(got))
	}
}

func TestIngestNDJSONLogsRefusesTooManyEntries(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	var b strings.Builder
	for i := 0; i <= maxLogEntries; i++ {
		b.WriteString(`{"message":"m` + strconv.Itoa(i) + `"}` + "\n")
	}
	rr := postLogs(t, server, "application/x-ndjson", b.String())
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over the entry cap = %d, want 413", rr.Code)
	}
}
