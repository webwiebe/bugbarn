package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestEnvironmentsListsProjectEnvironments(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)

	out, err := runCmd(t, cmdEnvironments, "--project", "default")
	if err != nil {
		t.Fatalf("empty list: %v\n%s", err, out)
	}
	var empty struct {
		Environments []map[string]any `json:"environments"`
	}
	if err := json.Unmarshal([]byte(out), &empty); err != nil || empty.Environments == nil || len(empty.Environments) != 0 {
		t.Fatalf("empty = %s (%v), want an empty list", out, err)
	}

	now := time.Now().UTC()
	pe := event.ProcessedEvent{
		Event: event.Event{
			ObservedAt: now, ReceivedAt: now.Add(time.Second), Severity: "ERROR", Message: "boom",
			Exception:  event.Exception{Type: "TestError", Message: "boom"},
			Attributes: map[string]any{"deployment.environment": "staging"},
		},
		Fingerprint: "fp-env", FingerprintMaterial: "TestError: boom",
	}
	if _, _, _, _, err := h.Store.PersistProcessedEvent(context.Background(), pe); err != nil {
		t.Fatalf("persist: %v", err)
	}
	out, err = runCmd(t, cmdEnvironments, "--project", "default")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	var got struct {
		Environments []struct {
			Environment string `json:"environment"`
			Issues      int    `json:"issues"`
		} `json:"environments"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(got.Environments) != 1 || got.Environments[0].Environment != "staging" || got.Environments[0].Issues != 1 {
		t.Fatalf("environments = %+v, want one staging row with one issue", got.Environments)
	}
}

func TestEnvironmentsReportsFailures(t *testing.T) {
	inEmptyDir(t)

	if _, err := runCmd(t, cmdEnvironments, "--no-such-flag"); err == nil {
		t.Error("an unknown flag must fail")
	}

	t.Setenv("BB_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := runCmd(t, cmdEnvironments); err == nil {
		t.Error("a missing config must fail")
	}

	srv := newScripted(t, 500, "boom")
	useConfig(t, apiKeyConfig(srv.URL))
	if _, err := runCmd(t, cmdEnvironments); err == nil || !strings.Contains(err.Error(), "HTTP 500: boom") {
		t.Errorf("a server error must surface, got %v", err)
	}
}

func TestProjectsMergeStampsTheEnvironment(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	ctx := context.Background()
	src, err := h.Store.CreateProject(ctx, "Legacy", "legacy-app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.DB().ExecContext(ctx,
		`INSERT INTO releases (project_id, name, observed_at, environment) VALUES (?, 'v1', '2026-01-01T00:00:00Z', '')`, src.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := runCmd(t, cmdProjects, "--merge", "legacy-app"); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("a merge without --into must print usage, got %v", err)
	}
	if _, err := runCmd(t, cmdProjects, "--merge", "default", "--into", "default"); err == nil {
		t.Error("merging a project into itself must fail")
	}
	if _, err := runCmd(t, cmdProjects, "--merge=legacy-app", "--into", "default", "--env", "bad env"); err == nil {
		t.Error("a bad environment must fail")
	}

	out, err := runCmd(t, cmdProjects, "--merge", "legacy-app", "--into", "default", "--env", "staging")
	if err != nil || !strings.Contains(out, `"ok"`) {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	def, err := h.Store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	var env string
	if err := h.Store.DB().QueryRowContext(ctx,
		`SELECT environment FROM releases WHERE project_id = ?`, def.ID).Scan(&env); err != nil || env != "staging" {
		t.Fatalf("release environment = %q (%v), want staging", env, err)
	}
}

func TestProjectsArchiveAndUnarchive(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	ctx := context.Background()
	if _, err := h.Store.CreateProject(ctx, "Legacy", "legacy-app"); err != nil {
		t.Fatal(err)
	}

	if _, err := runCmd(t, cmdProjects, "--archive", "a", "--unarchive", "b"); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("both flags must print usage, got %v", err)
	}
	out, err := runCmd(t, cmdProjects, "--archive=legacy-app")
	if err != nil || !strings.Contains(out, `"ok"`) {
		t.Fatalf("archive: %v\n%s", err, out)
	}
	if p, _ := h.Store.ProjectBySlug(ctx, "legacy-app"); p.Status != "archived" {
		t.Errorf("status = %q, want archived", p.Status)
	}
	if out, err = runCmd(t, cmdProjects, "--unarchive", "legacy-app"); err != nil || !strings.Contains(out, `"ok"`) {
		t.Fatalf("unarchive: %v\n%s", err, out)
	}
	if _, err := runCmd(t, cmdProjects, "--archive", "no-such-app"); err == nil {
		t.Error("archiving an unknown project must fail")
	}
}

func TestProjectsArchivedListsOnlyArchived(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	ctx := context.Background()
	for _, slug := range []string{"quiet-app", "live-app"} {
		if _, err := h.Store.CreateProject(ctx, slug, slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runCmd(t, cmdProjects, "--archive", "quiet-app"); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, cmdProjects, "--archived")
	if err != nil {
		t.Fatalf("archived: %v\n%s", err, out)
	}
	if !strings.Contains(out, "quiet-app") || strings.Contains(out, "live-app") {
		t.Errorf("--archived must list quiet-app only:\n%s", out)
	}
}

func TestProjectsMergeDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	ctx := context.Background()
	if _, err := h.Store.CreateProject(ctx, "Legacy", "legacy-app"); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, cmdProjects, "--merge", "legacy-app", "--into", "default", "--env", "staging", "--dry-run")
	if err != nil || !strings.Contains(out, `"dry_run": true`) && !strings.Contains(out, `"dry_run":true`) {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := h.Store.ProjectBySlug(ctx, "legacy-app"); err != nil {
		t.Errorf("a dry run must keep the source project: %v", err)
	}
}
