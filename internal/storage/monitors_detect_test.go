package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestListEnabledMonitorsCarriesProjectSlug(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	proj, err := s.CreateProject(ctx, "geo", "geo")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	if _, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{ProjectID: proj.ID, Slug: "sweep", Status: domain.CheckInOK, Interval: dur(time.Hour), At: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{ProjectID: proj.ID, Slug: "off", Status: domain.CheckInOK, Interval: dur(time.Hour), At: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE monitors SET enabled = 0 WHERE slug = 'off'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListEnabledMonitors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Slug != "sweep" || got[0].ProjectSlug != "geo" {
		t.Fatalf("enabled monitors: %+v", got)
	}
}

func TestMarkMonitorMissedOnlyWhenNothingCheckedIn(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	proj, err := s.CreateProject(ctx, "geo", "geo")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 13, 10, 0, 0, 123456789, time.UTC)
	res, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{ProjectID: proj.ID, Slug: "sweep", Status: domain.CheckInOK, Interval: dur(time.Hour), At: t0})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Monitor
	now := t0.Add(2 * time.Hour)

	// A check-in that landed after the scan makes the miss stale.
	applied, err := s.MarkMonitorMissed(ctx, m.ID, t0.Add(-time.Minute), now)
	if err != nil || applied {
		t.Fatalf("stale miss applied=%v err=%v", applied, err)
	}

	applied, err = s.MarkMonitorMissed(ctx, m.ID, m.LastCheckInAt, now)
	if err != nil || !applied {
		t.Fatalf("miss applied=%v err=%v", applied, err)
	}
	got, err := s.GetMonitor(ctx, proj.ID, "sweep")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.MonitorStateMissed || !got.StateSince.Equal(now) {
		t.Fatalf("after miss: %+v", got)
	}
	if !got.NeedsReport(now) {
		t.Fatal("a new miss needs a report")
	}

	// A second scan of the same state changes nothing.
	if applied, _ = s.MarkMonitorMissed(ctx, m.ID, m.LastCheckInAt, now.Add(time.Minute)); applied {
		t.Fatal("miss applied twice")
	}

	if err := s.MarkMonitorEmitted(ctx, m.ID, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMonitor(ctx, proj.ID, "sweep")
	if got.NeedsReport(now.Add(time.Hour)) || !got.NeedsReport(now.Add(25*time.Hour)) {
		t.Fatalf("report window wrong: %+v", got)
	}
}

func TestIssueByFingerprint(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	proj, err := s.CreateProject(ctx, "geo", "geo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueByFingerprint(ctx, proj.ID, "monitor:sweep:missed"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("unknown fingerprint: %v", err)
	}
}
