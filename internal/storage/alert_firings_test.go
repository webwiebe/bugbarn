package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestAlertFirings(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	alerts := store.Domains().Alerts
	projectID := store.DefaultProjectID()
	ctx := domain.WithProjectID(context.Background(), projectID)

	created, err := alerts.CreateAlert(ctx, domain.Alert{
		Name:            "On new issue",
		Enabled:         true,
		Condition:       "new_issue",
		CooldownMinutes: 15,
	})
	if err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}

	listed, err := alerts.ListAlertsForProject(context.Background(), projectID)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("ListAlertsForProject: got %+v, %v", listed, err)
	}
	if none, err := alerts.ListAlertsForProject(context.Background(), 0); err != nil || len(none) != 0 {
		t.Fatalf("ListAlertsForProject(0): got %+v, %v", none, err)
	}

	last, err := alerts.LastAlertFiring(context.Background(), created.ID, "issue-000001")
	if err != nil || !last.IsZero() {
		t.Fatalf("LastAlertFiring before any firing: got %v, %v", last, err)
	}

	before := time.Now().UTC().Truncate(time.Second)
	if err := alerts.RecordAlertFiring(context.Background(), created.ID, "issue-000001"); err != nil {
		t.Fatalf("RecordAlertFiring: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)
	last, err = alerts.LastAlertFiring(context.Background(), created.ID, "issue-000001")
	if err != nil {
		t.Fatalf("LastAlertFiring: %v", err)
	}
	if last.Before(before) || last.After(after) {
		t.Errorf("last firing %v outside [%v, %v]", last, before, after)
	}

	firedAt := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if err := alerts.UpdateAlertLastFired(context.Background(), created.ID, firedAt); err != nil {
		t.Fatalf("UpdateAlertLastFired: %v", err)
	}
	got, err := alerts.GetAlert(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if !got.LastFiredAt.Equal(firedAt) {
		t.Errorf("last_fired_at: got %v want %v", got.LastFiredAt, firedAt)
	}

	if err := alerts.UpdateAlertLastFired(context.Background(), "not-an-id", firedAt); err == nil {
		t.Error("UpdateAlertLastFired with a malformed ID: expected an error")
	}
}
