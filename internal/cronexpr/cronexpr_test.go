package cronexpr

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no tzdata for %s: %v", name, err)
	}
	return loc
}

func at(t *testing.T, loc *time.Location, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestNext(t *testing.T) {
	utc := time.UTC
	cases := []struct{ name, expr, after, want string }{
		{"every minute", "* * * * *", "2026-10-13 20:24", "2026-10-13 20:25"},
		{"strictly after", "30 6 * * *", "2026-10-13 06:30", "2026-10-14 06:30"},
		{"daily same day", "30 6 * * *", "2026-10-13 06:29", "2026-10-13 06:30"},
		{"step", "*/15 * * * *", "2026-10-13 20:46", "2026-10-13 21:00"},
		{"list and range", "0 9-11,15 * * *", "2026-10-13 11:30", "2026-10-13 15:00"},
		{"stepped range", "0 8-18/5 * * *", "2026-10-13 09:00", "2026-10-13 13:00"},
		{"weekdays", "0 6 * * 1-5", "2026-10-16 07:00", "2026-10-19 06:00"},
		{"sunday as 7", "0 6 * * 7", "2026-10-13 07:00", "2026-10-18 06:00"},
		{"weekday names", "0 6 * * mon,fri", "2026-10-13 07:00", "2026-10-16 06:00"},
		{"month names", "0 0 1 jan,jul *", "2026-10-13 07:00", "2027-01-01 00:00"},
		{"dom or dow when both set", "0 0 13 * fri", "2026-10-14 00:00", "2026-10-16 00:00"},
		{"leap day", "0 0 29 2 *", "2026-10-13 00:00", "2028-02-29 00:00"},
		{"year rollover", "59 23 31 12 *", "2026-12-31 23:59", "2027-12-31 23:59"},
		{"a/n runs to the end", "10/20 * * * *", "2026-10-13 20:00", "2026-10-13 20:10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			got := s.Next(at(t, utc, tc.after), utc)
			if want := at(t, utc, tc.want); !got.Equal(want) {
				t.Fatalf("Next(%s) = %s, want %s", tc.after, got, want)
			}
		})
	}
}

func TestParseRejectsBadExpressions(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *",
		"* * * 13 *", "* * * * 8", "*/0 * * * *", "a * * * *", "5-1 * * * *", "0 0 31 2 *", "@daily",
	} {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) accepted", expr)
		}
	}
}

func TestNextInTimeZone(t *testing.T) {
	ams := mustLoc(t, "Europe/Amsterdam")
	s, _ := Parse("0 6 * * *")
	got := s.Next(at(t, time.UTC, "2026-07-01 03:00"), ams) // 05:00 in Amsterdam (CEST)
	if want := at(t, ams, "2026-07-01 06:00"); !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
	if got.UTC().Hour() != 4 {
		t.Fatalf("06:00 CEST is 04:00 UTC, got %s", got.UTC())
	}
}

func TestDaylightSavingSpringForward(t *testing.T) {
	ams := mustLoc(t, "Europe/Amsterdam")
	// 2027-03-28: 02:00 jumps to 03:00 in Amsterdam, so 02:30 does not exist.
	s, _ := Parse("30 2 * * *")
	got := s.Next(at(t, ams, "2027-03-27 03:00"), ams)
	if want := time.Date(2027, 3, 28, 3, 30, 0, 0, ams); !got.Equal(want) {
		t.Fatalf("skipped time runs once, shifted forward: got %s want %s", got, want)
	}
	// The next run is the following day at 02:30 again, never twice on the gap day.
	next := s.Next(got, ams)
	if want := time.Date(2027, 3, 29, 2, 30, 0, 0, ams); !next.Equal(want) {
		t.Fatalf("got %s want %s", next, want)
	}
	// An hourly job loses the skipped hour and nothing else.
	hourly, _ := Parse("0 * * * *")
	got = hourly.Next(time.Date(2027, 3, 28, 1, 0, 0, 0, ams), ams)
	if want := time.Date(2027, 3, 28, 3, 0, 0, 0, ams); !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestDaylightSavingFallBack(t *testing.T) {
	ams := mustLoc(t, "Europe/Amsterdam")
	// 2026-10-25: 03:00 CEST falls back to 02:00 CET, so 02:30 happens twice.
	s, _ := Parse("30 2 * * *")
	first := s.Next(time.Date(2026, 10, 24, 12, 0, 0, 0, ams), ams)
	next := s.Next(first, ams)
	if next.Sub(first) < 24*time.Hour {
		t.Fatalf("the repeated hour must not run the job twice: %s then %s", first, next)
	}
	if first.Day() != 25 || next.Day() != 26 {
		t.Fatalf("got %s then %s", first, next)
	}
	// A job outside the repeated hour keeps its wall-clock time, so the 25-hour day shows.
	six, _ := Parse("0 6 * * *")
	a := six.Next(time.Date(2026, 10, 24, 5, 0, 0, 0, ams), ams)
	b := six.Next(a, ams)
	if a.Hour() != 6 || b.Hour() != 6 || b.Sub(a) != 25*time.Hour {
		t.Fatalf("got %s then %s", a, b)
	}
}
