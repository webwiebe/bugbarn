package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

func TestAnalyticsOverviewEmptyProject(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/overview", nil)
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Pageviews     int64 `json:"pageviews"`
		Sessions      int64 `json:"sessions"`
		Pages         int64 `json:"pages"`
		AvgDurationMs int64 `json:"avgDurationMs"`
	}
	decodeResponse(t, rr, &resp)

	if resp.Pageviews != 0 {
		t.Errorf("expected 0 pageviews, got %d", resp.Pageviews)
	}
	if resp.Sessions != 0 {
		t.Errorf("expected 0 sessions, got %d", resp.Sessions)
	}
	if resp.Pages != 0 {
		t.Errorf("expected 0 pages, got %d", resp.Pages)
	}
	if resp.AvgDurationMs != 0 {
		t.Errorf("expected 0 avgDurationMs, got %d", resp.AvgDurationMs)
	}
}

func TestAnalyticsTimelineDayGranularityZeroFills(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store)

	// Request exactly 3 days: 2026-04-01 to 2026-04-03.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/timeline?start=2026-04-01&end=2026-04-03&granularity=day", nil)
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Granularity string `json:"granularity"`
		Buckets     []struct {
			Date      string `json:"Date"`
			Pageviews int64  `json:"Pageviews"`
			Sessions  int64  `json:"Sessions"`
		} `json:"buckets"`
	}
	decodeResponse(t, rr, &resp)

	if resp.Granularity != "day" {
		t.Errorf("expected granularity=day, got %q", resp.Granularity)
	}

	// Zero-fill should produce exactly 3 buckets for the 3-day range.
	if len(resp.Buckets) != 3 {
		t.Errorf("expected 3 buckets, got %d", len(resp.Buckets))
	}
	want := []string{"2026-04-01", "2026-04-02", "2026-04-03"}
	for i, b := range resp.Buckets {
		if i >= len(want) {
			break
		}
		if b.Date != want[i] {
			t.Errorf("bucket[%d]: expected date %q, got %q", i, want[i], b.Date)
		}
		if b.Pageviews != 0 {
			t.Errorf("bucket[%d]: expected 0 pageviews, got %d", i, b.Pageviews)
		}
	}
}

func TestAnalyticsSegmentsUnknownDimReturnsEmpty(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/segments?dim=nonexistent_dimension", nil)
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Dim     string `json:"dim"`
		Buckets []any  `json:"buckets"`
	}
	decodeResponse(t, rr, &resp)

	if resp.Dim != "nonexistent_dimension" {
		t.Errorf("expected dim=nonexistent_dimension, got %q", resp.Dim)
	}
	if len(resp.Buckets) != 0 {
		t.Errorf("expected empty buckets, got %d", len(resp.Buckets))
	}
}

func TestAnalyticsUnauthenticatedReturns401(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	userAuth, err := auth.NewUserAuthenticator("admin", "secret", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager("test-secret", time.Hour)
	server := NewServerWithAuth(nil, store, userAuth, sessions, nil)

	endpoints := []string{
		"/api/v1/analytics/overview",
		"/api/v1/analytics/pages",
		"/api/v1/analytics/timeline",
		"/api/v1/analytics/referrers",
		"/api/v1/analytics/segments",
	}

	for _, path := range endpoints {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: expected 401, got %d", path, rr.Code)
		}
	}
}

func TestAnalyticsStartAfterEndReturns400(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	server := NewServer(nil, store)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/overview?start=2026-04-10&end=2026-04-01", nil)
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}
