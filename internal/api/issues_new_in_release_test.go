package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func TestListIssuesNewInRelease(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	firstSeen := map[string]time.Duration{
		"before": 5 * time.Minute,
		"inA":    20 * time.Minute,
		"inBoth": 45 * time.Minute,
		"inB":    70 * time.Minute,
	}
	ids := map[string]string{}
	for name, offset := range firstSeen {
		at := base.Add(offset)
		issue, _, _, _, err := store.PersistProcessedEvent(ctx, worker.ProcessedEvent{
			Event: event.Event{
				ObservedAt: at, ReceivedAt: at, Severity: "ERROR", Message: name,
				Exception: event.Exception{Type: "E" + name, Message: name},
			},
			Fingerprint: "new-in-release-" + name, FingerprintMaterial: name,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = issue.ID
	}

	create := func(name, env string, offset time.Duration) string {
		rel, err := store.CreateRelease(ctx, domain.Release{Name: name, Environment: env, ObservedAt: base.Add(offset)})
		if err != nil {
			t.Fatal(err)
		}
		return rel.ID
	}
	prodA := create("1.0.0", "production", 10*time.Minute)
	prodB := create("1.1.0", "production", 60*time.Minute)
	staging := create("1.0.5", "staging", 30*time.Minute)

	cases := []struct {
		name    string
		release string
		want    []string
	}{
		{"production window ends at the next production release", prodA, []string{"inA", "inBoth"}},
		{"latest production release is open ended", prodB, []string{"inB"}},
		{"staging release is not ended by production releases", staging, []string{"inBoth", "inB"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newInReleaseIDs(t, srv, tc.release)
			var want []string
			for _, name := range tc.want {
				want = append(want, ids[name])
			}
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("issues = %v, want %v", got, want)
			}
		})
	}

	rr := httptest.NewRecorder()
	srv.listIssues(rr, httptest.NewRequest(http.MethodGet, "/api/v1/issues?new_in_release=release-9999", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown release: status = %d, want 404", rr.Code)
	}
}

func newInReleaseIDs(t *testing.T, srv *Server, releaseID string) []string {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.listIssues(rr, httptest.NewRequest(http.MethodGet, "/api/v1/issues?status=all&new_in_release="+releaseID, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var resp struct {
		Issues             []domain.Issue `json:"issues"`
		TotalIsApproximate bool           `json:"totalIsApproximate"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.TotalIsApproximate {
		t.Error("totalIsApproximate = false, want true for a first-seen range")
	}
	var out []string
	for _, issue := range resp.Issues {
		out = append(out, issue.ID)
	}
	sort.Strings(out)
	return out
}
