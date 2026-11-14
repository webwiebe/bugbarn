package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestProjectsListAndCreate(t *testing.T) {
	newHarness(t)
	inEmptyDir(t)

	out, err := runCmd(t, cmdProjects, "--create", "My App")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"slug": "my-app"`) {
		t.Errorf("slug was not derived from the name:\n%s", out)
	}
	out, err = runCmd(t, cmdProjects, "--create", "Other", "--slug", "custom")
	if err != nil || !strings.Contains(out, `"slug": "custom"`) {
		t.Fatalf("create with slug: %v\n%s", err, out)
	}

	out, err = runCmd(t, cmdProjects)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Projects []struct {
			Slug       string `json:"slug"`
			EventCount *int   `json:"event_count"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(resp.Projects) != 3 {
		t.Errorf("projects = %d, want default + 2 created", len(resp.Projects))
	}
	// `bb projects` asks for usage counts explicitly, so every row carries one.
	for _, p := range resp.Projects {
		if p.EventCount == nil {
			t.Errorf("project %s has no event_count: the list must request ?usage=true", p.Slug)
		}
	}
}

func TestGroupsListAndCreate(t *testing.T) {
	newHarness(t)
	inEmptyDir(t)

	out, err := runCmd(t, cmdGroups, "--create", "Core Team")
	if err != nil || !strings.Contains(out, `"slug": "core-team"`) {
		t.Fatalf("create: %v\n%s", err, out)
	}
	out, err = runCmd(t, cmdGroups)
	if err != nil || !strings.Contains(out, "core-team") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if _, err := runCmd(t, cmdGroups, "--create", "Core Team"); err == nil || !strings.Contains(err.Error(), "HTTP 409") {
		t.Errorf("duplicate group: %v, want HTTP 409", err)
	}
	if _, err := runCmd(t, cmdGroups, "--bogus"); err == nil {
		t.Error("an unknown flag must fail")
	}
}

func TestAPIKeysListsKeys(t *testing.T) {
	newHarness(t)
	inEmptyDir(t)
	out, err := runCmd(t, cmdAPIKeys)
	if err != nil || !strings.Contains(out, "harness") {
		t.Fatalf("apikeys: %v\n%s", err, out)
	}
}

func TestMonitorsListAndDelete(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	proj, err := h.Store.ProjectBySlug(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	every := time.Hour
	if _, err := h.Store.RecordMonitorCheckIn(t.Context(), storage.MonitorCheckIn{
		ProjectID: proj.ID, Slug: "nightly", Status: "ok", Interval: &every, At: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, cmdMonitors, "--project", "default")
	if err != nil || !strings.Contains(out, "nightly") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	out, err = runCmd(t, cmdMonitors, "--project", "default", "--delete", "nightly")
	if err != nil || strings.TrimSpace(out) != "deleted monitor nightly" {
		t.Fatalf("delete: %v\n%s", err, out)
	}
	if _, err := runCmd(t, cmdMonitors, "--project", "default", "--delete", "nightly"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("deleting twice: %v, want HTTP 404", err)
	}
	if _, err := runCmd(t, cmdMonitors, "--delete", "nightly"); err == nil || !strings.Contains(err.Error(), "needs a project") {
		t.Errorf("delete without project: %v", err)
	}
	if _, err := runCmd(t, cmdMonitors, "--bogus"); err == nil {
		t.Error("an unknown flag must fail")
	}
}

func TestInitWritesLocalConfig(t *testing.T) {
	newHarness(t)
	dir := inEmptyDir(t)

	out, err := runCmd(t, cmdInit, "--project", "default")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"project": "default"`) {
		t.Errorf("stdout lacks the JSON result:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, localConfigFile))
	if err != nil || !strings.Contains(string(data), `"project": "default"`) {
		t.Fatalf("local config: %v %s", err, data)
	}
	lc, ok := findLocalConfig()
	if !ok || lc.Project != "default" {
		t.Errorf("findLocalConfig = %+v %v", lc, ok)
	}

	if _, err := runCmd(t, cmdInit, "--project", "a", "--group", "b"); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("both flags: %v", err)
	}
	if _, err := runCmd(t, cmdInit, "--project", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown project: %v", err)
	}
	if _, err := runCmd(t, cmdInit, "--group", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown group: %v", err)
	}
}

func TestInitGroupChoice(t *testing.T) {
	h := newHarness(t)
	dir := inEmptyDir(t)
	if _, err := h.Store.CreateGroup(t.Context(), "Team", "team"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, cmdInit, "--group", "team"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, localConfigFile))
	if !strings.Contains(string(data), `"group": "team"`) || strings.Contains(string(data), `"project"`) {
		t.Errorf("local config = %s", data)
	}
}

func TestFindLocalConfigWalksUpAndIgnoresEmpty(t *testing.T) {
	dir := inEmptyDir(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	if _, ok := findLocalConfig(); ok {
		t.Fatal("found a config in an empty tree")
	}
	if err := writeFile(dir, localConfigFile, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, ok := findLocalConfig(); ok {
		t.Error("an empty config must not count")
	}
	if err := writeFile(dir, localConfigFile, `{"project":"up"}`); err != nil {
		t.Fatal(err)
	}
	if lc, ok := findLocalConfig(); !ok || lc.Project != "up" {
		t.Errorf("findLocalConfig = %+v %v", lc, ok)
	}
	if err := writeFile(dir, localConfigFile, `not json`); err != nil {
		t.Fatal(err)
	}
	if _, ok := findLocalConfig(); ok {
		t.Error("a corrupt config must not count")
	}
}
