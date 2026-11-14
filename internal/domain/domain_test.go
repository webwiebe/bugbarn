package domain

import (
	"context"
	"testing"
	"time"
)

func TestProjectIDDistinguishesAllProjectsFromUnset(t *testing.T) {
	ctx := context.Background()
	if id, ok := ProjectIDFromContext(ctx); ok || id != 0 {
		t.Fatalf("empty context = %d, %v; want 0, false", id, ok)
	}
	if id, ok := ProjectIDFromContext(WithProjectID(ctx, 0)); !ok || id != 0 {
		t.Fatalf("explicit all-projects = %d, %v; want 0, true", id, ok)
	}
	if id, ok := ProjectIDFromContext(WithProjectID(ctx, 42)); !ok || id != 42 {
		t.Fatalf("project 42 = %d, %v", id, ok)
	}
}

func TestProjectIDsFromContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := ProjectIDsFromContext(ctx); ok {
		t.Fatal("empty context reports a group scope")
	}
	if _, ok := ProjectIDsFromContext(WithProjectIDs(ctx, nil)); ok {
		t.Fatal("an empty group must not count as a scope")
	}
	ids, ok := ProjectIDsFromContext(WithProjectIDs(ctx, []int64{3, 1}))
	if !ok || len(ids) != 2 || ids[0] != 3 || ids[1] != 1 {
		t.Fatalf("ids = %v, %v", ids, ok)
	}
}

func TestRequestInfoRoundTrip(t *testing.T) {
	if got := RequestInfoFromContext(context.Background()); got != (RequestInfo{}) {
		t.Fatalf("empty context = %+v, want zero value", got)
	}
	want := RequestInfo{Route: "GET /x", UserAgent: "curl/8"}
	if got := RequestInfoFromContext(WithRequestInfo(context.Background(), want)); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestIssueCountsFor(t *testing.T) {
	c := IssueCounts{Open: 1, Regressed: 2, Resolved: 3, Muted: 4, All: 10}
	for status, want := range map[string]int{
		"open": 1, "regressed": 2, "resolved": 3, "muted": 4, "all": 10, "": 10, "bogus": 10,
	} {
		if got := c.For(status); got != want {
			t.Errorf("For(%q) = %d, want %d", status, got, want)
		}
	}
}

func TestMonitorNeedsReport(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-2 * time.Hour)
	cases := []struct {
		name string
		m    Monitor
		want bool
	}{
		{"ok monitor has no incident", Monitor{State: MonitorStateOK}, false},
		{"incident never reported", Monitor{State: MonitorStateMissed, StateSince: since}, true},
		{"reported before this incident began", Monitor{State: MonitorStateMissed, StateSince: since, LastEmittedAt: since.Add(-time.Hour)}, true},
		{"reported for this incident an hour ago", Monitor{State: MonitorStateError, StateSince: since, LastEmittedAt: now.Add(-time.Hour)}, false},
		{"reported exactly a day ago", Monitor{State: MonitorStateMissed, StateSince: now.Add(-48 * time.Hour), LastEmittedAt: now.Add(-MonitorReportRepeat)}, true},
	}
	for _, tc := range cases {
		if got := tc.m.NeedsReport(now); got != tc.want {
			t.Errorf("%s: NeedsReport = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMonitorNextDueIntervalAndCreationFallback(t *testing.T) {
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m := Monitor{Interval: time.Hour, Grace: 5 * time.Minute, CreatedAt: created}
	if want := created.Add(65 * time.Minute); !m.NextDue().Equal(want) {
		t.Fatalf("before first check-in: %s, want %s", m.NextDue(), want)
	}
	m.LastCheckInAt = created.Add(3 * time.Hour)
	if want := m.LastCheckInAt.Add(65 * time.Minute); !m.NextDue().Equal(want) {
		t.Fatalf("after a check-in: %s, want %s", m.NextDue(), want)
	}
}

func TestMonitorNextDueCronUsesGraceAndZone(t *testing.T) {
	m := Monitor{
		Schedule: "0 6 * * *", Timezone: "UTC", Grace: 10 * time.Minute,
		LastCheckInAt: time.Date(2026, 11, 14, 6, 0, 5, 0, time.UTC),
	}
	if want := time.Date(2026, 11, 15, 6, 10, 0, 0, time.UTC); !m.NextDue().Equal(want) {
		t.Fatalf("NextDue = %s, want %s", m.NextDue(), want)
	}
}

func TestMonitorLocation(t *testing.T) {
	if (Monitor{}).Location() != time.UTC {
		t.Fatal("empty timezone should be UTC")
	}
	if (Monitor{Timezone: "Not/AZone"}).Location() != time.UTC {
		t.Fatal("unknown timezone should fall back to UTC")
	}
	if got := (Monitor{Timezone: "Etc/GMT+2"}).Location().String(); got != "Etc/GMT+2" {
		t.Fatalf("zone = %q", got)
	}
}
