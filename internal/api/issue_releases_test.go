package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func TestIssueReleasesEndpoint(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	var issueID string
	for i, release := range []string{"0.2.0", "0.2.0", "0.3.0", ""} {
		attrs := map[string]any{}
		if release != "" {
			attrs["release"] = release
		}
		at := base.Add(time.Duration(i) * time.Minute)
		issue, _, _, _, err := store.PersistProcessedEvent(context.Background(), worker.ProcessedEvent{
			Event: event.Event{
				ObservedAt: at, ReceivedAt: at.Add(time.Second), Severity: "ERROR",
				Message: "boom", Exception: event.Exception{Type: "Boom", Message: "boom"}, Attributes: attrs,
			},
			Fingerprint: "api-releases-fingerprint", FingerprintMaterial: "Boom: boom",
		})
		if err != nil {
			t.Fatal(err)
		}
		issueID = issue.ID
	}

	rr := httptest.NewRecorder()
	srv.serveIssueRoute(rr, httptest.NewRequest(http.MethodGet, "/api/v1/issues/"+issueID+"/releases", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var resp struct {
		Releases []domain.IssueRelease `json:"releases"`
		Sampled  int                   `json:"sampled_events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Sampled != 4 || len(resp.Releases) != 2 ||
		resp.Releases[0].Release != "0.3.0" || resp.Releases[1].Release != "0.2.0" || resp.Releases[1].Events != 2 {
		t.Fatalf("response = %+v", resp)
	}

	rr = httptest.NewRecorder()
	srv.serveIssueRoute(rr, httptest.NewRequest(http.MethodGet, "/api/v1/issues/NOPE-999/releases", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown issue: status = %d, want 404", rr.Code)
	}
}

func TestIssueReleasesEndpointEmptyList(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	issue := persistTestIssue(t, store)
	rr := httptest.NewRecorder()
	srv.serveIssueRoute(rr, httptest.NewRequest(http.MethodGet, "/api/v1/issues/"+issue.ID+"/releases", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["releases"]) != "[]" {
		t.Fatalf("releases = %s, want []", raw["releases"])
	}
}
