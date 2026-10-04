package config

import "path/filepath"

// Telemetry configures the infrastructure telemetry files (internal/telemetrydb).
type Telemetry struct {
	// SecurityDBPath is BUGBARN_SECURITY_DB_PATH; defaults to security.db next
	// to BUGBARN_DB_PATH, so it lands on the same volume with no manifest change.
	SecurityDBPath string
	// SecurityMaxBytes is BUGBARN_SECURITY_MAX_BYTES, the hard size cap of
	// security.db (default 2GiB). Raw security logs are best-effort; the
	// oldest rows are evicted to stay under it.
	SecurityMaxBytes int64
	// MetricsDBPath is BUGBARN_METRICS_DB_PATH; defaults to metrics.db next to
	// BUGBARN_DB_PATH.
	MetricsDBPath string
	// MetricsMaxBytes is BUGBARN_METRICS_MAX_BYTES (default 256MiB).
	MetricsMaxBytes int64
}

func parseTelemetryConfig(dbPath string) Telemetry {
	dir := filepath.Dir(dbPath)
	return Telemetry{
		SecurityDBPath:   getenv("BUGBARN_SECURITY_DB_PATH", filepath.Join(dir, "security.db")),
		SecurityMaxBytes: envInt64Positive("BUGBARN_SECURITY_MAX_BYTES", 2<<30),
		MetricsDBPath:    getenv("BUGBARN_METRICS_DB_PATH", filepath.Join(dir, "metrics.db")),
		MetricsMaxBytes:  envInt64Positive("BUGBARN_METRICS_MAX_BYTES", 256<<20),
	}
}
