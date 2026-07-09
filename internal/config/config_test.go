package config

import (
	"testing"
	"time"
)

func TestEnvIntInRange(t *testing.T) {
	cases := []struct {
		name, val        string
		fallback, lo, hi int
		want             int
	}{
		{"unset uses fallback", "", 8, 0, 23, 8},
		{"in range", "5", 8, 0, 23, 5},
		{"below range rejected", "-1", 0, 0, 6, 0},
		{"above range rejected", "7", 0, 0, 6, 0},
		{"non-numeric rejected", "x", 8, 0, 23, 8},
		{"boundary low", "0", 8, 0, 23, 0},
		{"boundary high", "23", 8, 0, 23, 23},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.val != "" {
				t.Setenv("TEST_RANGE", c.val)
			}
			if got := envIntInRange("TEST_RANGE", c.fallback, c.lo, c.hi); got != c.want {
				t.Errorf("envIntInRange(%q)=%d want %d", c.val, got, c.want)
			}
		})
	}
}

func TestEnvInt64Positive(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		if got := envInt64Positive("TEST_I64", 1<<20); got != 1<<20 {
			t.Errorf("got %d", got)
		}
	})
	t.Run("zero rejected", func(t *testing.T) {
		t.Setenv("TEST_I64", "0")
		if got := envInt64Positive("TEST_I64", 42); got != 42 {
			t.Errorf("zero should be rejected, got %d", got)
		}
	})
	t.Run("positive accepted", func(t *testing.T) {
		t.Setenv("TEST_I64", "4096")
		if got := envInt64Positive("TEST_I64", 42); got != 4096 {
			t.Errorf("got %d", got)
		}
	})
}

func TestEnvDurationSeconds(t *testing.T) {
	t.Run("fallback", func(t *testing.T) {
		if got := envDurationSeconds("TEST_TTL", 12*time.Hour); got != 12*time.Hour {
			t.Errorf("got %v", got)
		}
	})
	t.Run("seconds", func(t *testing.T) {
		t.Setenv("TEST_TTL", "90")
		if got := envDurationSeconds("TEST_TTL", 12*time.Hour); got != 90*time.Second {
			t.Errorf("got %v", got)
		}
	})
}

func TestParseTrustedProxies(t *testing.T) {
	got := parseTrustedProxies("10.0.0.0/8, 192.168.1.1 , , bogus")
	if len(got) != 2 {
		t.Fatalf("expected 2 valid CIDRs, got %d: %v", len(got), got)
	}
	if got[1].String() != "192.168.1.1/32" {
		t.Errorf("bare IP should become /32, got %s", got[1].String())
	}
}

func TestLoadDefaults(t *testing.T) {
	// Isolate from any inherited BUGBARN_* env by clearing the ones we assert on.
	for _, k := range []string{"BUGBARN_MODE", "BUGBARN_MAX_BODY_BYTES", "BUGBARN_ANALYTICS_RETENTION_DAYS", "BUGBARN_SESSION_TTL_SECONDS"} {
		t.Setenv(k, "")
	}
	cfg := Load()
	if cfg.MaxBodyBytes != 1<<20 {
		t.Errorf("MaxBodyBytes default = %d", cfg.MaxBodyBytes)
	}
	if cfg.SessionTTL != 12*time.Hour {
		t.Errorf("SessionTTL default = %v", cfg.SessionTTL)
	}
	if cfg.AnalyticsRetentionDays != 90 {
		t.Errorf("AnalyticsRetentionDays default = %d", cfg.AnalyticsRetentionDays)
	}
	if cfg.Digest.Mail.Port != 587 {
		t.Errorf("SMTP port default = %d", cfg.Digest.Mail.Port)
	}
}
