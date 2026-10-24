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
	// The TTL is the ABSOLUTE session cap since sessions became token-bound;
	// day-to-day validity tracks the short-lived IdP access token instead.
	if cfg.SessionTTL != 12*time.Hour {
		t.Errorf("SessionTTL default = %v", cfg.SessionTTL)
	}
	if cfg.OIDCRefreshGrace != time.Hour {
		t.Errorf("OIDCRefreshGrace default = %v", cfg.OIDCRefreshGrace)
	}
	if cfg.AnalyticsRetentionDays != 90 {
		t.Errorf("AnalyticsRetentionDays default = %d", cfg.AnalyticsRetentionDays)
	}
	if cfg.Digest.Mail.Port != 587 {
		t.Errorf("SMTP port default = %d", cfg.Digest.Mail.Port)
	}
}

// The checkpoint knobs let production restore the pre-bugbarn#236 behavior
// (TRUNCATE every tick, 10s wait) without a rebuild.
func TestLoadWALTruncateKnobs(t *testing.T) {
	t.Setenv("BUGBARN_WAL_TRUNCATE_EVERY", "")
	t.Setenv("BUGBARN_WAL_TRUNCATE_BUSY_TIMEOUT_MS", "")
	cfg := Load()
	if cfg.WALTruncateEvery != 10 || cfg.WALTruncateBusyTimeout != time.Second {
		t.Errorf("defaults = every %d, wait %v; want 10 and 1s", cfg.WALTruncateEvery, cfg.WALTruncateBusyTimeout)
	}

	if cfg.WALWarnBytes != 256<<20 {
		t.Errorf("WALWarnBytes default = %d, want 256 MiB", cfg.WALWarnBytes)
	}
	t.Setenv("BUGBARN_WAL_WARN_BYTES", "1048576")
	if got := Load().WALWarnBytes; got != 1<<20 {
		t.Errorf("WALWarnBytes override = %d", got)
	}

	t.Setenv("BUGBARN_WAL_TRUNCATE_EVERY", "1")
	t.Setenv("BUGBARN_WAL_TRUNCATE_BUSY_TIMEOUT_MS", "10000")
	cfg = Load()
	if cfg.WALTruncateEvery != 1 || cfg.WALTruncateBusyTimeout != 10*time.Second {
		t.Errorf("legacy values = every %d, wait %v; want 1 and 10s", cfg.WALTruncateEvery, cfg.WALTruncateBusyTimeout)
	}

	t.Setenv("BUGBARN_WAL_TRUNCATE_EVERY", "0")
	t.Setenv("BUGBARN_WAL_TRUNCATE_BUSY_TIMEOUT_MS", "-5")
	cfg = Load()
	if cfg.WALTruncateEvery != 10 || cfg.WALTruncateBusyTimeout != time.Second {
		t.Errorf("invalid values should fall back to defaults, got every %d, wait %v", cfg.WALTruncateEvery, cfg.WALTruncateBusyTimeout)
	}
}

func TestDefaultFromName(t *testing.T) {
	cases := map[string]string{
		"":           "BugBarn",
		"production": "BugBarn",
		"Production": "BugBarn",
		"prod":       "BugBarn",
		"staging":    "BugBarn (staging)",
		"testing":    "BugBarn (testing)",
	}
	for env, want := range cases {
		if got := defaultFromName(env); got != want {
			t.Errorf("defaultFromName(%q) = %q want %q", env, got, want)
		}
	}
}

func TestMailIdentityFromEnv(t *testing.T) {
	t.Run("name derives from the environment", func(t *testing.T) {
		t.Setenv("BUGBARN_ENVIRONMENT", "staging")
		t.Setenv("SMTP_FROM_NAME", "")
		t.Setenv("SMTP_REPLY_TO", "ops@example.com")
		cfg := Load()
		if cfg.Digest.Mail.FromName != "BugBarn (staging)" {
			t.Errorf("FromName = %q", cfg.Digest.Mail.FromName)
		}
		if cfg.Digest.Mail.ReplyTo != "ops@example.com" {
			t.Errorf("ReplyTo = %q", cfg.Digest.Mail.ReplyTo)
		}
	})
	t.Run("SMTP_FROM_NAME overrides the derived name", func(t *testing.T) {
		t.Setenv("BUGBARN_ENVIRONMENT", "staging")
		t.Setenv("SMTP_FROM_NAME", "Custom Sender")
		cfg := Load()
		if cfg.Digest.Mail.FromName != "Custom Sender" {
			t.Errorf("FromName = %q", cfg.Digest.Mail.FromName)
		}
	})
}

func TestEnvironmentAndIsProduction(t *testing.T) {
	t.Run("BUGBARN_ENVIRONMENT wins over BUGBARN_ENV", func(t *testing.T) {
		t.Setenv("BUGBARN_ENV", "staging")
		t.Setenv("BUGBARN_ENVIRONMENT", "production")
		cfg := Load()
		if cfg.Environment != "production" || !cfg.IsProduction() {
			t.Errorf("Environment = %q IsProduction = %v", cfg.Environment, cfg.IsProduction())
		}
	})
	t.Run("falls back to BUGBARN_ENV", func(t *testing.T) {
		t.Setenv("BUGBARN_ENVIRONMENT", "")
		t.Setenv("BUGBARN_ENV", "testing")
		cfg := Load()
		if cfg.Environment != "testing" || cfg.IsProduction() {
			t.Errorf("Environment = %q IsProduction = %v", cfg.Environment, cfg.IsProduction())
		}
	})
	t.Run("case-insensitive production", func(t *testing.T) {
		if !(Config{Environment: "Production"}).IsProduction() {
			t.Error("IsProduction should be case-insensitive")
		}
	})
}
