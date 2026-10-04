package config

import (
	"testing"
	"time"
)

func TestCloudflareDisabledByDefault(t *testing.T) {
	t.Setenv("BUGBARN_CLOUDFLARE_API_TOKEN", "")
	t.Setenv("BUGBARN_CLOUDFLARE_ZONE_IDS", "")
	t.Setenv("BUGBARN_CLOUDFLARE_POLL_INTERVAL", "")
	got := parseCloudflareConfig()
	if got.Enabled() || got.PollInterval != time.Minute {
		t.Fatalf("got %+v", got)
	}
}

func TestCloudflareEnabledNeedsTokenAndZones(t *testing.T) {
	t.Setenv("BUGBARN_CLOUDFLARE_API_TOKEN", "tok")
	t.Setenv("BUGBARN_CLOUDFLARE_ZONE_IDS", "")
	if parseCloudflareConfig().Enabled() {
		t.Fatal("enabled without zones")
	}
	t.Setenv("BUGBARN_CLOUDFLARE_ZONE_IDS", " z1, z2 ,")
	got := parseCloudflareConfig()
	if !got.Enabled() || len(got.ZoneIDs) != 2 || got.ZoneIDs[1] != "z2" {
		t.Fatalf("got %+v", got)
	}
}

func TestCloudflarePollInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"":      time.Minute,
		"2m":    2 * time.Minute,
		"90":    90 * time.Second,
		"1s":    time.Minute,
		"bogus": time.Minute,
	}
	for in, want := range cases {
		if got := parsePollInterval(in); got != want {
			t.Errorf("parsePollInterval(%q) = %s, want %s", in, got, want)
		}
	}
}
