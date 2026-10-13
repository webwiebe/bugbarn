package domain

import (
	"testing"
	"time"
)

// A job that runs at 02:30 Amsterdam time: on the spring-forward night 02:30
// does not exist, so the run lands at 03:30 and the day after goes back to 02:30.
func TestMonitorNextDueAcrossDaylightSaving(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip(err)
	}
	m := Monitor{Schedule: "30 2 * * *", Timezone: "Europe/Amsterdam", Grace: 10 * time.Minute}

	m.LastCheckInAt = time.Date(2027, 3, 27, 2, 30, 20, 0, ams)
	if want := time.Date(2027, 3, 28, 3, 40, 0, 0, ams); !m.NextDue().Equal(want) {
		t.Fatalf("spring forward: NextDue = %s, want %s", m.NextDue(), want)
	}
	m.LastCheckInAt = time.Date(2027, 3, 28, 3, 30, 20, 0, ams)
	if want := time.Date(2027, 3, 29, 2, 40, 0, 0, ams); !m.NextDue().Equal(want) {
		t.Fatalf("after the shifted run: NextDue = %s, want %s", m.NextDue(), want)
	}
}

func TestMonitorNextDueFallsBackToIntervalOnABrokenSchedule(t *testing.T) {
	at := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	m := Monitor{Schedule: "not a cron", Interval: time.Hour, Grace: time.Minute, LastCheckInAt: at}
	if want := at.Add(61 * time.Minute); !m.NextDue().Equal(want) {
		t.Fatalf("NextDue = %s, want %s", m.NextDue(), want)
	}
}

func TestMonitorLocationDefaultsToUTC(t *testing.T) {
	for _, tz := range []string{"", "Nowhere/Land"} {
		if got := (Monitor{Timezone: tz}).Location(); got != time.UTC {
			t.Errorf("zone %q resolved to %v", tz, got)
		}
	}
}
