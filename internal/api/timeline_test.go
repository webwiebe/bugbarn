package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// seedTimelineError inserts one error event for a project, received now.
func seedTimelineError(t *testing.T, store *storage.Store, projectID int64, fingerprint string) {
	t.Helper()
	ctx := context.Background()
	res, err := store.DB().ExecContext(ctx, `
		INSERT INTO issues (project_id, fingerprint, fingerprint_material, title, normalized_title,
			exception_type, first_seen, last_seen, event_count, representative_event_json, issue_number)
		VALUES (?, ?, '', 'issue', 'issue', 'Error', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1, '{}', 1)`,
		projectID, fingerprint)
	if err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	issueID, _ := res.LastInsertId()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO events (project_id, issue_id, fingerprint, received_at, observed_at, severity, message, event_json)
		VALUES (?, ?, 'fp', ?, ?, 'ERROR', 'boom', '{}')`, projectID, issueID, ts, ts); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func getTimeline(t *testing.T, server http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	return rr
}

type timelineBody struct {
	Range         string `json:"range"`
	BucketSeconds int64  `json:"bucket_seconds"`
	Buckets       []struct {
		Start  string           `json:"start"`
		Counts map[string]int64 `json:"counts"`
	} `json:"buckets"`
	Projects []struct {
		Slug  string `json:"slug"`
		Total int64  `json:"total"`
	} `json:"projects"`
}

func TestErrorTimelineEndpointCountsAllProjects(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	ctx := context.Background()
	alpha, err := store.EnsureProject(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.EnsureProject(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	seedTimelineError(t, store, alpha.ID, "a1")
	seedTimelineError(t, store, alpha.ID, "a2")
	seedTimelineError(t, store, beta.ID, "b1")
	server := NewServer(nil, store, nil)

	rr := getTimeline(t, server, "/api/v1/events/timeline")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body timelineBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Range != "24h" || body.BucketSeconds != 3600 || len(body.Buckets) != 24 {
		t.Fatalf("default range = %s, %d s, %d buckets; want 24h hourly", body.Range, body.BucketSeconds, len(body.Buckets))
	}
	last := body.Buckets[len(body.Buckets)-1].Counts
	if last["alpha"] != 2 || last["beta"] != 1 {
		t.Errorf("current bucket = %v, want alpha 2 and beta 1", last)
	}
	if len(body.Projects) != 2 || body.Projects[0].Slug != "alpha" || body.Projects[0].Total != 2 {
		t.Errorf("projects = %+v", body.Projects)
	}
}

func TestErrorTimelineEndpointRangeParameter(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	for target, want := range map[string]struct {
		buckets int
		seconds int64
	}{
		"/api/v1/events/timeline?range=24h": {24, 3600},
		"/api/v1/events/timeline?range=7d":  {28, 6 * 3600},
		"/api/v1/events/timeline?range=30d": {30, 86400},
	} {
		rr := getTimeline(t, server, target)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", target, rr.Code, rr.Body.String())
		}
		var body timelineBody
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s decode: %v", target, err)
		}
		if len(body.Buckets) != want.buckets || body.BucketSeconds != want.seconds {
			t.Errorf("%s = %d buckets of %d s, want %d of %d", target, len(body.Buckets), body.BucketSeconds, want.buckets, want.seconds)
		}
		for i, b := range body.Buckets {
			if b.Counts == nil {
				t.Fatalf("%s bucket %d has no counts object; empty buckets must be {}", target, i)
			}
		}
	}

	rr := getTimeline(t, server, "/api/v1/events/timeline?range=90d")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("range=90d status = %d, want 400", rr.Code)
	}
}

func TestErrorTimelineEndpointHonoursProjectScope(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	ctx := context.Background()
	alpha, _ := store.EnsureProject(ctx, "alpha")
	beta, _ := store.EnsureProject(ctx, "beta")
	seedTimelineError(t, store, alpha.ID, "a1")
	seedTimelineError(t, store, beta.ID, "b1")
	server := NewServer(nil, store, nil)

	// The pipeline puts the project of an API key or X-BugBarn-Project header in
	// the request context; the handler's scope is whatever it finds there.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/timeline", nil)
	req = req.WithContext(domain.WithProjectID(req.Context(), beta.ID))
	rr := httptest.NewRecorder()
	server.serveErrorTimeline(rr, req)
	var body timelineBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if len(body.Projects) != 1 || body.Projects[0].Slug != "beta" {
		t.Errorf("projects = %+v, want only beta", body.Projects)
	}
}
