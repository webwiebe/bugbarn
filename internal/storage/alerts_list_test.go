package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// ListAlerts returns the alerts of the context's project, newest first, with
// the project slug filled in, and leaves other projects' alerts out.
func TestListAlertsScopesToProjectNewestFirst(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	alerts := store.Domains().Alerts
	ctx := context.Background()

	other, err := store.Domains().Projects.CreateProject(ctx, "Other", "other")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	mainCtx := domain.WithProjectID(ctx, store.DefaultProjectID())
	otherCtx := domain.WithProjectID(ctx, other.ID)

	first, err := alerts.CreateAlert(mainCtx, domain.Alert{Name: "first", Enabled: true, Condition: "new_issue"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := alerts.CreateAlert(mainCtx, domain.Alert{Name: "second", Enabled: true, Condition: "new_issue"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alerts.CreateAlert(otherCtx, domain.Alert{Name: "elsewhere", Enabled: true, Condition: "new_issue"}); err != nil {
		t.Fatal(err)
	}

	got, err := alerts.ListAlerts(mainCtx)
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("ListAlerts = %+v, want [second, first]", got)
	}
	if got[0].ProjectSlug == "" {
		t.Fatalf("ProjectSlug is empty, want the project's slug")
	}

	gotOther, err := alerts.ListAlerts(otherCtx)
	if err != nil || len(gotOther) != 1 || gotOther[0].ProjectSlug != "other" {
		t.Fatalf("ListAlerts(other) = %+v, %v", gotOther, err)
	}
}
