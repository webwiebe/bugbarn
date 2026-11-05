package storage

import (
	"testing"
	"time"
)

func TestHourBucketIndex(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)
	onTheHour := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		now      time.Time
		bucket   string
		wantIdx  int
		inWindow bool
	}{
		{"current hour", now, "2026-11-05T10", 23, true},
		{"previous hour", now, "2026-11-05T09", 22, true},
		{"23 hours ago is the oldest bucket", now, "2026-11-04T11", 0, true},
		{"24 hours ago is out of the window", now, "2026-11-04T10", 0, false},
		{"less than an hour ahead counts as the current hour", now, "2026-11-05T11", 23, true},
		{"an hour or more ahead is out of the window", now, "2026-11-05T12", 0, false},
		{"exactly 24 hours ago on the hour is out", onTheHour, "2026-11-04T10", 0, false},
		{"exactly 23 hours ago on the hour is the oldest bucket", onTheHour, "2026-11-04T11", 0, true},
		{"across midnight", time.Date(2026, 11, 5, 0, 15, 0, 0, time.UTC), "2026-11-04T23", 22, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx, inWindow, err := hourBucketIndex(tc.now, tc.bucket)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if idx != tc.wantIdx || inWindow != tc.inWindow {
				t.Fatalf("got (%d, %v), want (%d, %v)", idx, inWindow, tc.wantIdx, tc.inWindow)
			}
		})
	}
}

func TestHourBucketIndexMalformed(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)
	for _, bucket := range []string{"", "garbage", "2026-11-05", "2026-11-05T25"} {
		if _, inWindow, err := hourBucketIndex(now, bucket); err == nil || inWindow {
			t.Errorf("bucket %q: err = %v, inWindow = %v; want an error", bucket, err, inWindow)
		}
	}
}
