package config

import "testing"

func TestTelemetryDefaultsFollowDBPath(t *testing.T) {
	t.Setenv("BUGBARN_SECURITY_DB_PATH", "")
	t.Setenv("BUGBARN_METRICS_DB_PATH", "")
	t.Setenv("BUGBARN_SECURITY_MAX_BYTES", "")
	t.Setenv("BUGBARN_METRICS_MAX_BYTES", "")
	got := parseTelemetryConfig("/var/lib/bugbarn/bugbarn.db")
	if got.SecurityDBPath != "/var/lib/bugbarn/security.db" || got.MetricsDBPath != "/var/lib/bugbarn/metrics.db" {
		t.Fatalf("paths = %q, %q", got.SecurityDBPath, got.MetricsDBPath)
	}
	if got.SecurityMaxBytes != 2<<30 || got.MetricsMaxBytes != 256<<20 {
		t.Fatalf("caps = %d, %d", got.SecurityMaxBytes, got.MetricsMaxBytes)
	}
}

func TestTelemetryOverrides(t *testing.T) {
	t.Setenv("BUGBARN_SECURITY_DB_PATH", "/data/sec.db")
	t.Setenv("BUGBARN_SECURITY_MAX_BYTES", "1048576")
	got := parseTelemetryConfig("/var/lib/bugbarn/bugbarn.db")
	if got.SecurityDBPath != "/data/sec.db" || got.SecurityMaxBytes != 1<<20 {
		t.Fatalf("got %+v", got)
	}
}
