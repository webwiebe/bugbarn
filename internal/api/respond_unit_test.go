package api

import (
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"My App":        "my-app",
		"  Foo  Bar  ":  "foo-bar",
		"a--b__c":       "a-b-c",
		"Trailing!!!":   "trailing",
		"!!!Leading":    "leading",
		"already-slug1": "already-slug1",
		"":              "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWeekStartIsSundayMidnightUTC(t *testing.T) {
	t.Parallel()
	wed := time.Date(2026, 11, 11, 15, 30, 0, 0, time.UTC)
	want := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	if got := weekStart(wed); !got.Equal(want) {
		t.Errorf("weekStart = %v, want %v", got, want)
	}
	if got := weekStart(want); !got.Equal(want) {
		t.Errorf("weekStart of a Sunday = %v, want itself", got)
	}
}

func TestIsoWeekNumberCountsFromJanuaryFirst(t *testing.T) {
	t.Parallel()
	cases := []struct {
		day  time.Time
		want int
	}{
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0},
		{time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), 0},
		{time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC), 1},
		{time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), 52},
	}
	for _, c := range cases {
		if got := isoWeekNumber(c.day); got != c.want {
			t.Errorf("isoWeekNumber(%v) = %d, want %d", c.day.Format("2006-01-02"), got, c.want)
		}
	}
}

func TestPickLogBucket(t *testing.T) {
	t.Parallel()
	if _, err := pickLogBucket(time.Hour, time.Millisecond); err == nil {
		t.Error("a bucket under one second should be refused")
	}
	if _, err := pickLogBucket(1000*time.Hour, time.Minute); err == nil {
		t.Error("a bucket that gives more than the cap should be refused")
	}
	if got, err := pickLogBucket(time.Hour, time.Minute); err != nil || got != time.Minute {
		t.Errorf("requested bucket = %v, %v; want 1m, nil", got, err)
	}
	short, err := pickLogBucket(time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	long, err := pickLogBucket(365*24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if long <= short {
		t.Errorf("a year (%v) should use a wider step than an hour (%v)", long, short)
	}
}

func TestClientIPTrustsForwardedForOnlyFromProxies(t *testing.T) {
	t.Parallel()
	_, proxy, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{trustedProxies: []*net.IPNet{proxy}}

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.1.2.3:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.1.2.3")
	if got := s.clientIP(req); got != "203.0.113.9" {
		t.Errorf("trusted proxy: clientIP = %q, want the first forwarded address", got)
	}

	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := s.clientIP(req); got != "203.0.113.9" {
		t.Errorf("single forwarded address: clientIP = %q", got)
	}

	req.RemoteAddr = "198.51.100.7:5555"
	if got := s.clientIP(req); got != "198.51.100.7" {
		t.Errorf("untrusted peer: clientIP = %q, want its own address", got)
	}

	req.RemoteAddr = "no-port"
	if got := remoteHost(req.RemoteAddr); got != "no-port" {
		t.Errorf("remoteHost without a port = %q", got)
	}
}
