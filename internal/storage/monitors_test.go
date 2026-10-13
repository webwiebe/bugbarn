package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func dur(d time.Duration) *time.Duration { return &d }

func TestMonitorCheckInCreatesThenUpdates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	proj, err := s.CreateProject(ctx, "geo", "geo")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)

	res, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{
		ProjectID: proj.ID, Slug: "sweep", Status: domain.CheckInOK, Interval: dur(time.Hour), Grace: dur(10 * time.Minute), At: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Monitor.State != domain.MonitorStateOK || res.Monitor.Interval != time.Hour || res.Monitor.Grace != 10*time.Minute {
		t.Fatalf("first check-in: %+v", res)
	}
	if want := t0.Add(70 * time.Minute); !res.Monitor.NextDue().Equal(want) {
		t.Fatalf("next due %v, want %v", res.Monitor.NextDue(), want)
	}

	// A later check-in without timings keeps the stored configuration.
	t1 := t0.Add(time.Hour)
	res, err = s.RecordMonitorCheckIn(ctx, MonitorCheckIn{ProjectID: proj.ID, Slug: "sweep", Status: domain.CheckInOK, At: t1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.Monitor.Interval != time.Hour || !res.Monitor.LastCheckInAt.Equal(t1) {
		t.Fatalf("second check-in: %+v", res)
	}

	// Sent timings replace the stored ones.
	res, err = s.RecordMonitorCheckIn(ctx, MonitorCheckIn{
		ProjectID: proj.ID, Slug: "sweep", Status: domain.CheckInOK, Interval: dur(2 * time.Hour), At: t1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Monitor.Interval != 2*time.Hour || res.Monitor.Grace != 10*time.Minute {
		t.Fatalf("updated timings: %+v", res.Monitor)
	}
}

func TestMonitorCheckInStateFollowsStatus(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	proj, _ := s.CreateProject(ctx, "geo", "geo")
	t0 := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	in := MonitorCheckIn{ProjectID: proj.ID, Slug: "worker", Interval: dur(time.Minute), At: t0}

	in.Status = domain.CheckInOK
	if _, err := s.RecordMonitorCheckIn(ctx, in); err != nil {
		t.Fatal(err)
	}

	in.Status, in.At = domain.CheckInError, t0.Add(time.Minute)
	res, _ := s.RecordMonitorCheckIn(ctx, in)
	if res.PreviousState != domain.MonitorStateOK || res.Monitor.State != domain.MonitorStateError ||
		!res.Monitor.StateSince.Equal(in.At) {
		t.Fatalf("error check-in: %+v", res)
	}

	// A second error keeps the state and its start.
	in.At = t0.Add(2 * time.Minute)
	res, _ = s.RecordMonitorCheckIn(ctx, in)
	if res.PreviousState != domain.MonitorStateError || !res.Monitor.StateSince.Equal(t0.Add(time.Minute)) {
		t.Fatalf("repeated error: %+v", res)
	}

	in.Status, in.At = domain.CheckInOK, t0.Add(3*time.Minute)
	res, _ = s.RecordMonitorCheckIn(ctx, in)
	if res.PreviousState != domain.MonitorStateError || res.Monitor.State != domain.MonitorStateOK {
		t.Fatalf("recovery: %+v", res)
	}
}

func TestMonitorCheckInUnknownWithoutIntervalIsNotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	proj, _ := s.CreateProject(ctx, "geo", "geo")
	_, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{ProjectID: proj.ID, Slug: "ghost", Status: domain.CheckInOK, At: time.Now()})
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("unknown monitor: %v", err)
	}
	if got, _ := s.ListMonitors(ctx, proj.ID); len(got) != 0 {
		t.Fatalf("a rejected check-in created %d monitors", len(got))
	}
}

func TestMonitorLimitAndIsolation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a, _ := s.CreateProject(ctx, "a", "a")
	b, _ := s.CreateProject(ctx, "b", "b")
	now := time.Now().UTC()
	for i := 0; i < MaxMonitorsPerProject; i++ {
		_, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{
			ProjectID: a.ID, Slug: fmt.Sprintf("job-%03d", i), Status: domain.CheckInOK, Interval: dur(time.Hour), At: now,
		})
		if err != nil {
			t.Fatalf("monitor %d: %v", i, err)
		}
	}
	_, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{ProjectID: a.ID, Slug: "one-too-many", Status: domain.CheckInOK, Interval: dur(time.Hour), At: now})
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("over the limit: %v", err)
	}
	// The limit is per project, and an existing monitor still accepts check-ins.
	if _, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{ProjectID: b.ID, Slug: "job-000", Status: domain.CheckInOK, Interval: dur(time.Hour), At: now}); err != nil {
		t.Fatalf("other project: %v", err)
	}
	if _, err := s.RecordMonitorCheckIn(ctx, MonitorCheckIn{ProjectID: a.ID, Slug: "job-000", Status: domain.CheckInOK, At: now}); err != nil {
		t.Fatalf("existing monitor at the limit: %v", err)
	}
	listA, _ := s.ListMonitors(ctx, a.ID)
	listB, _ := s.ListMonitors(ctx, b.ID)
	if len(listA) != MaxMonitorsPerProject || len(listB) != 1 {
		t.Fatalf("lists: %d and %d", len(listA), len(listB))
	}
	if _, err := s.GetMonitor(ctx, b.ID, "job-001"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("monitor of another project: %v", err)
	}
}
