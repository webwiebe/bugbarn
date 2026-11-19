package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestArchiveProjectRefusesARecentlyActiveProject(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	p, err := store.CreateProject(ctx, "Busy", "busy-app")
	if err != nil {
		t.Fatal(err)
	}
	recent := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	mustExec(ctx, t, store,
		`INSERT INTO log_entries (project_id, received_at, message, data_json) VALUES (?, ?, 'hi', '{}')`, p.ID, recent)

	last, err := store.ArchiveProject(ctx, "busy-app", 7*24*time.Hour)
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("err = %v, want conflict", err)
	}
	if last.IsZero() {
		t.Error("the refusal must still report the last activity")
	}
	got, _ := store.ProjectBySlug(ctx, "busy-app")
	if got.Status == domain.ProjectStatusArchived {
		t.Error("a busy project must stay active")
	}
}

func TestArchiveProjectReportsTheNewestOfEventsLogsAndPageViews(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	p, err := store.CreateProject(ctx, "Quiet", "quiet-app")
	if err != nil {
		t.Fatal(err)
	}
	oldLog := time.Now().UTC().AddDate(0, 0, -30).Truncate(time.Second)
	newestView := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Second)
	mustExec(ctx, t, store,
		`INSERT INTO log_entries (project_id, received_at, message, data_json) VALUES (?, ?, 'old', '{}')`,
		p.ID, oldLog.Format(time.RFC3339Nano))
	mustExec(ctx, t, store,
		`INSERT INTO analytics_pageviews (project_id, ts, pathname) VALUES (?, ?, '/')`, p.ID, newestView.Unix())

	last, err := store.ArchiveProject(ctx, "quiet-app", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !last.Equal(newestView) {
		t.Errorf("last activity = %s, want %s", last, newestView)
	}
	got, _ := store.ProjectBySlug(ctx, "quiet-app")
	if got.Status != domain.ProjectStatusArchived {
		t.Errorf("status = %q, want archived", got.Status)
	}

	if err := store.UnarchiveProject(ctx, "quiet-app"); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if got, _ = store.ProjectBySlug(ctx, "quiet-app"); got.Status != "active" {
		t.Errorf("status = %q, want active", got.Status)
	}
	if err := store.UnarchiveProject(ctx, "quiet-app"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unarchiving an active project: err = %v, want not found", err)
	}
}

func TestArchiveProjectWithNoActivityAndUnknownSlug(t *testing.T) {
	t.Parallel()
	store, ctx := openMergeStore(t)
	if _, err := store.CreateProject(ctx, "Empty", "empty-app"); err != nil {
		t.Fatal(err)
	}
	last, err := store.ArchiveProject(ctx, "empty-app", 7*24*time.Hour)
	if err != nil || !last.IsZero() {
		t.Fatalf("empty project: last=%v err=%v; want zero time and no error", last, err)
	}
	if _, err := store.ArchiveProject(ctx, "no-such-app", time.Hour); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown slug: err = %v, want not found", err)
	}
}
