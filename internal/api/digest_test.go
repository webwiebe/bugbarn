package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// countingDigestStore records whether a digest run started; it has no projects,
// so a run gathers nothing and delivers nothing.
type countingDigestStore struct{ lists int }

func (c *countingDigestStore) ListProjects(context.Context) ([]domain.Project, error) {
	c.lists++
	return nil, nil
}

func (c *countingDigestStore) WeeklyDigest(context.Context, int64, time.Time) (domain.DigestData, error) {
	return domain.DigestData{}, nil
}

func TestDigestTriggerBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		wantCode int
		wantRun  bool
	}{
		{"empty body sends to the configured channels", "", http.StatusOK, true},
		{"a recipient override sends", `{"to":"ops@example.com"}`, http.StatusOK, true},
		{"a wrongly typed field is refused", `{"to":5}`, http.StatusBadRequest, false},
		{"broken JSON is refused", `{"to":`, http.StatusBadRequest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &countingDigestStore{}
			s := &Server{}
			s.SetDigest(digest.Config{WebhookURL: "http://127.0.0.1:1/unused"}, store)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/digest", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			s.serveDigestTrigger(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if ran := store.lists > 0; ran != tc.wantRun {
				t.Fatalf("digest ran = %v, want %v", ran, tc.wantRun)
			}
		})
	}
}
