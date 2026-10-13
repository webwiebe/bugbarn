package fingerprint

import (
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestNormalizeTimestamps(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, in, want string }{
		{"utc with Z", "got '2026-08-21T16:29:46Z'", "got '<ts>'"},
		{"fractional seconds", "at 2026-08-21T16:29:46.123456Z end", "at <ts> end"},
		{"comma fraction", "at 2026-08-21 16:29:46,500 end", "at <ts> end"},
		{"offset with colon", "2026-08-21T16:29:46+02:00 failed", "<ts> failed"},
		{"offset without colon", "2026-08-21T16:29:46-0500 failed", "<ts> failed"},
		{"space separator", "2026-08-21 16:29:46 failed", "<ts> failed"},
		{"minutes only", "2026-08-21T16:29 failed", "<ts> failed"},
		{"date only", "expired on 2026-08-21.", "expired on <ts>."},
		{"two timestamps", "from 2026-08-21T01:02:03Z to 2026-08-22T04:05:06Z", "from <ts> to <ts>"},
		{"plain numbers stay numbers", "retry 3 of 12", "retry <num> of <num>"},
		{"version is not a date", "node 22.4.1", "node <num>.<num>.<num>"},
	}
	for _, tc := range cases {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("%s: normalize(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestFingerprintGroupsMessagesDifferingOnlyByTimestamp(t *testing.T) {
	t.Parallel()

	msg := func(ts string) event.Event {
		return event.Event{Exception: event.Exception{
			Type:    "DataError",
			Message: "invalid input for query argument $12: '" + ts + "' (expected a datetime.date or datetime.datetime instance, got 'str')",
		}}
	}
	first := msg("2026-08-21T16:29:46Z")
	for _, ts := range []string{"2026-08-22T03:04:05Z", "2025-01-31T23:59:59.999Z", "2026-08-21T16:29:46+02:00"} {
		if Fingerprint(first) != Fingerprint(msg(ts)) {
			t.Errorf("timestamp %s split the issue:\n%s\n%s", ts, Material(first), Material(msg(ts)))
		}
	}
}

func TestFingerprintKeepsDifferentErrorsApartDespiteTimestamps(t *testing.T) {
	t.Parallel()

	a := event.Event{Exception: event.Exception{Type: "DataError", Message: "bad value '2026-08-21T16:29:46Z'"}}
	b := event.Event{Exception: event.Exception{Type: "DataError", Message: "stale value '2026-08-21T16:29:46Z'"}}
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("different messages must keep different fingerprints")
	}
}
