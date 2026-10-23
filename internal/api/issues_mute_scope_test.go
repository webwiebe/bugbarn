package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// bb mute sends PATCH /issues/<id>/mute with an API key. Without an
// X-BugBarn-Project header the request context holds the default project, not
// the issue's. The mute must still land and read back as muted (bugbarn#232).
func TestMuteEndpointReadsBackMutedAcrossProjects(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	issue := persistTestIssue(t, store)
	other, err := store.CreateProject(context.Background(), "Other", "other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	ctx := domain.WithProjectID(context.Background(), other.ID)

	body, _ := json.Marshal(map[string]string{"mute_mode": "until_regression"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+issue.ID+"/mute", bytes.NewReader(body)).WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.muteIssue(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("mute status = %d (%s)", rr.Code, rr.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v1/issues/"+issue.ID, http.NoBody)
	grr := httptest.NewRecorder()
	srv.getIssue(grr, get)
	var resp struct {
		Issue storage.Issue `json:"issue"`
	}
	if err := json.NewDecoder(grr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Issue.Status != "muted" || resp.Issue.MuteMode != "until_regression" {
		t.Fatalf("read back status=%q mode=%q, want muted/until_regression", resp.Issue.Status, resp.Issue.MuteMode)
	}
}
