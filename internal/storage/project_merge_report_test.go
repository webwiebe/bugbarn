package storage

import (
	"errors"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func TestMergeProjectsReportDryRunRollsBack(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	source, err := store.CreateProject(ctx, "Source", "source-proj")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, "Target", "target-proj"); err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, store,
		`INSERT INTO releases (project_id, name, observed_at, environment) VALUES (?, 'v1', '2026-01-01T00:00:00Z', '')`, source.ID)

	rep, err := store.MergeProjectsReport(ctx, "source-proj", "target-proj", "dev", true)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || rep.Moved["releases"] != 1 || rep.AlreadyMerged {
		t.Errorf("dry run report = %+v", rep)
	}
	if _, err := store.ProjectBySlug(ctx, "source-proj"); err != nil {
		t.Fatalf("dry run must keep the source: %v", err)
	}

	rep, err = store.MergeProjectsReport(ctx, "source-proj", "target-proj", "dev", false)
	if err != nil || rep.DryRun || rep.Moved["releases"] != 1 {
		t.Fatalf("merge report = %+v, %v", rep, err)
	}
	if _, err := store.ProjectBySlug(ctx, "source-proj"); err == nil {
		t.Fatal("the source must be gone after the merge")
	}

	rep, err = store.MergeProjectsReport(ctx, "source-proj", "target-proj", "dev", false)
	if err != nil || !rep.AlreadyMerged || rep.Moved["releases"] != 0 {
		t.Fatalf("re-run report = %+v, %v", rep, err)
	}
	_, err = store.MergeProjectsReport(ctx, "ghost", "target-proj", "", false)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown source: %v", err)
	}
}
