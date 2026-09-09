package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func putLimits(t *testing.T, srv *Server, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+slug+"/limits", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.updateProjectLimits(rr, req)
	return rr
}

func TestUpdateProjectLimitsSetsRetentionAndSampling(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	srv.globalRetentionDays = 30

	rr := putLimits(t, srv, "default", `{"retention_days":7,"sampling_mode":"off"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}

	p, err := store.ProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if p.RetentionDays == nil || *p.RetentionDays != 7 {
		t.Errorf("retention_days = %v, want 7", p.RetentionDays)
	}
	if p.SamplingMode != "off" {
		t.Errorf("sampling_mode = %q, want %q", p.SamplingMode, "off")
	}
}

// A window longer than the deployment's is capped rather than rejected: the
// global sweep deletes past it regardless, so storing the larger number would be
// a promise we cannot keep. The response says so.
func TestUpdateProjectLimitsClampsToTheGlobalWindow(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	srv.globalRetentionDays = 30

	rr := putLimits(t, srv, "default", `{"retention_days":365,"sampling_mode":""}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		RetentionDays *int `json:"retention_days"`
		Clamped       bool `json:"clamped"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Clamped {
		t.Error("response did not report the value as clamped")
	}
	if resp.RetentionDays == nil || *resp.RetentionDays != 30 {
		t.Errorf("clamped to %v, want 30", resp.RetentionDays)
	}

	p, err := store.ProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if p.RetentionDays == nil || *p.RetentionDays != 30 {
		t.Errorf("stored retention_days = %v, want 30", p.RetentionDays)
	}
}

// null means "inherit the deployment window" and must clear a previous override.
func TestUpdateProjectLimitsNullClearsTheOverride(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	srv.globalRetentionDays = 30

	if rr := putLimits(t, srv, "default", `{"retention_days":5,"sampling_mode":"on"}`); rr.Code != http.StatusOK {
		t.Fatalf("seed: status %d", rr.Code)
	}
	if rr := putLimits(t, srv, "default", `{"retention_days":null,"sampling_mode":""}`); rr.Code != http.StatusOK {
		t.Fatalf("clear: status %d", rr.Code)
	}

	p, err := store.ProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if p.RetentionDays != nil {
		t.Errorf("retention_days = %v, want nil (inherit)", *p.RetentionDays)
	}
	if p.SamplingMode != "" {
		t.Errorf("sampling_mode = %q, want empty (inherit)", p.SamplingMode)
	}
}

func TestUpdateProjectLimitsRejectsBadInput(t *testing.T) {
	t.Parallel()

	srv, _ := setupTestServer(t)
	srv.globalRetentionDays = 30

	cases := []struct {
		name string
		body string
		want int
	}{
		{"unknown sampling mode", `{"sampling_mode":"sometimes"}`, http.StatusBadRequest},
		{"zero retention", `{"retention_days":0}`, http.StatusBadRequest},
		{"negative retention", `{"retention_days":-5}`, http.StatusBadRequest},
		{"malformed json", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rr := putLimits(t, srv, "default", tc.body); rr.Code != tc.want {
				t.Errorf("status %d, want %d (body %s)", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestUpdateProjectLimitsUnknownProjectIs404(t *testing.T) {
	t.Parallel()

	srv, _ := setupTestServer(t)
	srv.globalRetentionDays = 30

	if rr := putLimits(t, srv, "no-such-project", `{"retention_days":7}`); rr.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 (body %s)", rr.Code, rr.Body.String())
	}
}

// The project list is what the volume UI reads: it must carry each project's
// policy and the defaults an unset project inherits.
func TestListProjectsCarriesLimitsAndDefaults(t *testing.T) {
	t.Parallel()

	srv, _ := setupTestServer(t)
	srv.globalRetentionDays = 30

	if rr := putLimits(t, srv, "default", `{"retention_days":7,"sampling_mode":"off"}`); rr.Code != http.StatusOK {
		t.Fatalf("seed: status %d", rr.Code)
	}

	rr := httptest.NewRecorder()
	srv.listProjects(rr, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}

	var resp struct {
		Projects []struct {
			Slug          string `json:"slug"`
			RetentionDays *int   `json:"retention_days"`
			SamplingMode  string `json:"sampling_mode"`
		} `json:"projects"`
		Defaults struct {
			RetentionDays int   `json:"retention_days"`
			SampleAfter   int64 `json:"sample_after"`
		} `json:"defaults"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Defaults.RetentionDays != 30 {
		t.Errorf("defaults.retention_days = %d, want 30", resp.Defaults.RetentionDays)
	}
	if resp.Defaults.SampleAfter <= 0 {
		t.Errorf("defaults.sample_after = %d, want the deployment threshold", resp.Defaults.SampleAfter)
	}
	var found bool
	for _, p := range resp.Projects {
		if p.Slug != "default" {
			continue
		}
		found = true
		if p.RetentionDays == nil || *p.RetentionDays != 7 {
			t.Errorf("retention_days = %v, want 7", p.RetentionDays)
		}
		if p.SamplingMode != "off" {
			t.Errorf("sampling_mode = %q, want off", p.SamplingMode)
		}
	}
	if !found {
		t.Error("default project missing from the list")
	}
}
