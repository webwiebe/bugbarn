package config

import (
	"net"
	"os"
	"path/filepath"
)

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
	// MetricsRawRetentionDays is BUGBARN_METRICS_RAW_RETENTION_DAYS (default
	// 7): how long per-minute host samples are kept.
	MetricsRawRetentionDays int
	// MetricsHourlyRetentionDays is BUGBARN_METRICS_HOURLY_RETENTION_DAYS
	// (default 90): how long hourly rollups are kept. Hosts that have not
	// reported for this long are dropped from the host list.
	MetricsHourlyRetentionDays int
	// Project is BUGBARN_TELEMETRY_PROJECT (default "infra"): ingest-scoped
	// keys of this project may post telemetry, and detections file their
	// issues in it.
	Project string
	// TrustedGateways is BUGBARN_TELEMETRY_TRUSTED_GATEWAYS, a comma-separated
	// list of addresses or CIDRs of reverse proxies in front of Traefik (the
	// Caddy gateway for the staging and test hosts). Security rules group on the
	// client address, which is only known from X-Forwarded-For when the peer is
	// one of these; empty means that header is never used.
	TrustedGateways []*net.IPNet
}

func parseTelemetryConfig(dbPath string) Telemetry {
	dir := filepath.Dir(dbPath)
	return Telemetry{
		SecurityDBPath:   getenv("BUGBARN_SECURITY_DB_PATH", filepath.Join(dir, "security.db")),
		SecurityMaxBytes: envInt64Positive("BUGBARN_SECURITY_MAX_BYTES", 2<<30),
		MetricsDBPath:    getenv("BUGBARN_METRICS_DB_PATH", filepath.Join(dir, "metrics.db")),
		MetricsMaxBytes:  envInt64Positive("BUGBARN_METRICS_MAX_BYTES", 256<<20),
		Project:          getenv("BUGBARN_TELEMETRY_PROJECT", "infra"),
		TrustedGateways:  parseTrustedProxies(os.Getenv("BUGBARN_TELEMETRY_TRUSTED_GATEWAYS")),

		MetricsRawRetentionDays:    envIntPositive("BUGBARN_METRICS_RAW_RETENTION_DAYS", 7),
		MetricsHourlyRetentionDays: envIntPositive("BUGBARN_METRICS_HOURLY_RETENTION_DAYS", 90),
	}
}
