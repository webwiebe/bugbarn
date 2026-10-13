package monitorwatch

import (
	"context"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	monitorsvc "github.com/wiebe-xyz/bugbarn/internal/service/monitors"
)

func (h *harness) cronCheckIn(status string, withSchedule bool) {
	h.t.Helper()
	in := monitorsvc.CheckInInput{Slug: "sweep", Status: status}
	if withSchedule {
		in.Schedule, in.Timezone, in.Grace = "0 6 * * *", "Europe/Amsterdam", dur(10*time.Minute)
	}
	if _, err := h.svc.CheckIn(context.Background(), h.project.ID, in); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) setClock(t time.Time) {
	h.clock = t
	h.detector.Check(context.Background())
}

func utc(day, hour, minute int) time.Time {
	return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC)
}

// A daily 06:00 Amsterdam job (04:00 UTC in October): silent until the grace
// ends, missed after it, answered by a late ok, then missed again as a regression.
func TestCronMonitorMissThenLateOkThenRegression(t *testing.T) {
	h := newHarness(t)
	h.cronCheckIn("ok", true) // created on 13 October 10:00 UTC
	m := h.monitor()
	if m.Schedule != "0 6 * * *" || m.Timezone != "Europe/Amsterdam" || m.Interval != 0 {
		t.Fatalf("stored schedule: %+v", m)
	}
	if want := utc(14, 4, 10); !m.NextDue().Equal(want) {
		t.Fatalf("NextDue = %s, want %s", m.NextDue(), want)
	}

	h.setClock(utc(14, 4, 5)) // the run is due but inside the grace
	if got := h.monitor().State; got != domain.MonitorStateOK {
		t.Fatalf("state inside grace = %s", got)
	}

	h.setClock(utc(14, 4, 15))
	if got := h.monitor().State; got != domain.MonitorStateMissed {
		t.Fatalf("state after grace = %s", got)
	}
	if is := h.issue(domain.MonitorStateMissed); is.Status != "unresolved" {
		t.Fatalf("missed issue status = %s", is.Status)
	}

	h.clock = utc(14, 7, 0) // the job finally ran, three hours late
	h.cronCheckIn("ok", false)
	m = h.monitor()
	if m.State != domain.MonitorStateOK {
		t.Fatalf("state after late ok = %s", m.State)
	}
	if is := h.issue(domain.MonitorStateMissed); is.Status != "resolved" {
		t.Fatalf("the late ok must resolve the issue, status = %s", is.Status)
	}
	if want := utc(15, 4, 10); !m.NextDue().Equal(want) {
		t.Fatalf("after the late ok the next run is the one of the 15th: NextDue = %s, want %s", m.NextDue(), want)
	}

	h.setClock(utc(15, 4, 5))
	if got := h.monitor().State; got != domain.MonitorStateOK {
		t.Fatalf("state before the next run's grace ends = %s", got)
	}
	h.setClock(utc(15, 4, 15))
	if got := h.monitor().State; got != domain.MonitorStateMissed {
		t.Fatalf("state after a second miss = %s", got)
	}
	if is := h.issue(domain.MonitorStateMissed); is.RegressionCount != 1 {
		t.Fatalf("the same issue regresses: %+v", is)
	}
}

// Switching a monitor between modes by check-in options.
func TestMonitorSwitchesBetweenIntervalAndSchedule(t *testing.T) {
	h := newHarness(t)
	h.checkIn("ok", true) // interval mode
	h.cronCheckIn("ok", true)
	if m := h.monitor(); m.Schedule == "" || m.Interval != 0 {
		t.Fatalf("schedule must replace the interval: %+v", m)
	}
	h.checkIn("ok", true)
	if m := h.monitor(); m.Schedule != "" || m.Timezone != "" || m.Interval != time.Hour {
		t.Fatalf("interval must replace the schedule: %+v", m)
	}
}
