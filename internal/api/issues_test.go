package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	alertsvc "github.com/wiebe-xyz/bugbarn/internal/service/alerts"
	analyticssvc "github.com/wiebe-xyz/bugbarn/internal/service/analytics"
	issuesvc "github.com/wiebe-xyz/bugbarn/internal/service/issues"
	logsvc "github.com/wiebe-xyz/bugbarn/internal/service/logs"
	projectsvc "github.com/wiebe-xyz/bugbarn/internal/service/projects"
	releasesvc "github.com/wiebe-xyz/bugbarn/internal/service/releases"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func setupTestServer(t *testing.T) (*Server, *storage.Store) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return &Server{
		issues:    issuesvc.New(store, nil),
		projects:  projectsvc.New(store, nil),
		releases:  releasesvc.New(store, nil),
		alerts:    alertsvc.New(store, nil),
		logs:      logsvc.New(store, nil),
		analytics: analyticssvc.New(store, nil),
	}, store
}

func persistTestIssue(t *testing.T, store *storage.Store) storage.Issue {
	t.Helper()
	pe := worker.ProcessedEvent{
		Event: event.Event{
			ObservedAt: time.Now().UTC(),
			ReceivedAt: time.Now().UTC().Add(time.Second),
			Severity:   "ERROR",
			Message:    "api issues test error",
			Exception:  event.Exception{Type: "TestError", Message: "api issues test error"},
		},
		Fingerprint:         "test-api-issues-fingerprint",
		FingerprintMaterial: "TestError: api issues test error",
	}
	issue, _, _, _, err := store.PersistProcessedEvent(context.Background(), pe)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	return issue
}

func TestMuteIssueEndpoint(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	issue := persistTestIssue(t, store)

	cases := []struct {
		name       string
		muteMode   string
		wantStatus int
	}{
		{"until_regression", "until_regression", http.StatusOK},
		{"forever", "forever", http.StatusOK},
		{"invalid_mode", "never", http.StatusBadRequest},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"mute_mode": tc.muteMode})
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.ID+"/mute", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.muteIssue(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d (body: %s)", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var resp struct {
					Issue storage.Issue `json:"issue"`
				}
				if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if resp.Issue.Status != "muted" {
					t.Errorf("expected status 'muted', got %q", resp.Issue.Status)
				}
				if resp.Issue.MuteMode != tc.muteMode {
					t.Errorf("expected MuteMode %q, got %q", tc.muteMode, resp.Issue.MuteMode)
				}
			}
		})
	}
}

func TestUnmuteIssueEndpoint(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	issue := persistTestIssue(t, store)

	// First mute the issue.
	if _, err := store.MuteIssue(context.Background(), issue.ID, "forever"); err != nil {
		t.Fatalf("mute: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.ID+"/unmute", http.NoBody)
	rr := httptest.NewRecorder()
	srv.unmuteIssue(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Issue storage.Issue `json:"issue"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Issue.Status != "unresolved" {
		t.Errorf("expected status 'unresolved', got %q", resp.Issue.Status)
	}
}

func TestListIssuesIncludesHourlyCounts(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	persistTestIssue(t, store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)
	rr := httptest.NewRecorder()
	srv.listIssues(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	issues, ok := resp["issues"].([]any)
	if !ok {
		t.Fatalf("expected issues array, got %T", resp["issues"])
	}
	if len(issues) == 0 {
		t.Fatal("expected at least one issue")
	}

	_, ok = issues[0].(map[string]any)
	if !ok {
		t.Fatal("expected issue to be an object")
	}

	if _, ok := resp["hasMore"]; !ok {
		t.Fatal("expected hasMore field in response")
	}
}

func persistTestIssueWithFingerprint(t *testing.T, store *storage.Store, fp, message string) storage.Issue {
	t.Helper()
	pe := worker.ProcessedEvent{
		Event: event.Event{
			ObservedAt: time.Now().UTC(),
			ReceivedAt: time.Now().UTC().Add(time.Second),
			Severity:   "ERROR",
			Message:    message,
			Exception:  event.Exception{Type: "TestError", Message: message},
		},
		Fingerprint:         fp,
		FingerprintMaterial: "TestError: " + message,
	}
	issue, _, _, _, err := store.PersistProcessedEvent(context.Background(), pe)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	return issue
}

func TestListIssuesPagination(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	for i := 0; i < 5; i++ {
		persistTestIssueWithFingerprint(t, store, "fp-paginate-"+string(rune('a'+i)), "error "+string(rune('a'+i)))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues?limit=3", nil)
	rr := httptest.NewRecorder()
	srv.listIssues(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	issues := resp["issues"].([]any)
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(issues))
	}
	if resp["hasMore"] != true {
		t.Fatal("expected hasMore=true")
	}

	// Second page
	req = httptest.NewRequest(http.MethodGet, "/api/v1/issues?limit=3&offset=3", nil)
	rr = httptest.NewRecorder()
	srv.listIssues(rr, req)

	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	issues = resp["issues"].([]any)
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues on second page, got %d", len(issues))
	}
	if resp["hasMore"] != false {
		t.Fatal("expected hasMore=false on last page")
	}
}

func TestListIssuesRegressedFirst(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	ctx := context.Background()

	// Create two issues with distinct fingerprints.
	issueA := persistTestIssueWithFingerprint(t, store, "fp-regress-a", "normal error")
	issueB := persistTestIssueWithFingerprint(t, store, "fp-regress-b", "will regress error")

	// Mute issueB with until_regression, then trigger a regression.
	if _, err := store.MuteIssue(ctx, issueB.ID, "until_regression"); err != nil {
		t.Fatalf("mute: %v", err)
	}
	pe := worker.ProcessedEvent{
		Event: event.Event{
			ObservedAt: time.Now().UTC(),
			ReceivedAt: time.Now().UTC().Add(time.Second),
			Severity:   "ERROR",
			Message:    "will regress error",
			Exception:  event.Exception{Type: "TestError", Message: "will regress error"},
		},
		Fingerprint:         "fp-regress-b",
		FingerprintMaterial: "TestError: will regress error",
	}
	regressedIssue, _, _, regressed, err := store.PersistProcessedEvent(ctx, pe)
	if err != nil {
		t.Fatalf("persist regression: %v", err)
	}
	if !regressed {
		t.Fatal("expected regression")
	}

	// Query with status=open — regressed should come first.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues?status=open", nil)
	rr := httptest.NewRecorder()
	srv.listIssues(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	issues := resp["issues"].([]any)
	if len(issues) < 2 {
		t.Fatalf("expected at least 2 issues, got %d", len(issues))
	}

	firstIssue := issues[0].(map[string]any)
	firstStatus, _ := firstIssue["Status"].(string)
	if firstStatus != "regressed" {
		t.Errorf("expected first issue to be regressed, got %q", firstStatus)
	}

	_ = issueA
	_ = regressedIssue
}
