package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/detect"
)

type ruleView struct {
	ID         string  `json:"id"`
	Threshold  float64 `json:"threshold"`
	Builtin    bool    `json:"builtin"`
	Overridden bool    `json:"overridden"`
}

func listRules(t *testing.T, server *Server) []ruleView {
	t.Helper()
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/detection/rules", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status %d body=%q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Rules []ruleView `json:"rules"`
	}
	decodeResponse(t, rr, &resp)
	return resp.Rules
}

func ruleByID(rules []ruleView, id string) (ruleView, bool) {
	for _, r := range rules {
		if r.ID == id {
			return r, true
		}
	}
	return ruleView{}, false
}

func TestDetectionRulesAPI(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)
	var reloaded []detect.Rule
	server.DetectionRules().OnChange(func(r []detect.Rule) { reloaded = r })

	if got := listRules(t, server); len(got) != len(detect.Defaults()) {
		t.Fatalf("fresh list has %d rules, want %d built-ins", len(got), len(detect.Defaults()))
	}

	put := func(id, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/detection/rules/"+id, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(rr, req)
		return rr
	}
	override := `{"name":"Disk almost full","enabled":true,"severity":"ERROR","track":"metrics",` +
		`"metric":"fs.*.used_pct","op":"gt","threshold":80,"for":"10m","cooldown":"6h"}`
	if rr := put("disk-full", override); rr.Code != http.StatusOK {
		t.Fatalf("put status %d body=%q", rr.Code, rr.Body.String())
	}
	if r, _ := ruleByID(listRules(t, server), "disk-full"); r.Threshold != 80 || !r.Builtin || !r.Overridden {
		t.Fatalf("after override: %+v", r)
	}
	if len(reloaded) == 0 {
		t.Fatal("engine was not reloaded")
	}

	if rr := put("disk-full", `{"name":"x","severity":"LOUD","track":"metrics"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid rule status %d, want 400", rr.Code)
	}

	del := func(id string) int {
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/detection/rules/"+id, nil))
		return rr.Code
	}
	if code := del("disk-full"); code != http.StatusOK {
		t.Fatalf("delete status %d", code)
	}
	if r, _ := ruleByID(listRules(t, server), "disk-full"); r.Overridden || r.Threshold == 80 {
		t.Fatalf("delete did not restore the built-in: %+v", r)
	}
	if code := del("disk-full"); code != http.StatusNotFound {
		t.Fatalf("second delete status %d, want 404", code)
	}
}
