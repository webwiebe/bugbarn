package config

import (
	"bufio"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
)

// Config holds all runtime configuration for the BugBarn server.
type Config struct {
	Addr                   string
	APIKey                 string
	APIKeySHA256           string
	AdminUsername          string
	AdminPassword          string
	AdminPasswordBcrypt    string
	SessionSecret          string
	SessionTTL             time.Duration
	AllowedOrigins         []string
	TrustedProxies         []*net.IPNet
	SpoolDir               string
	DBPath                 string
	MaxBodyBytes           int64
	MaxSpoolBytes          int64
	MaxSourceMapBytes      int64
	PublicURL              string
	Environment            string // BUGBARN_ENV: this instance's environment (production/staging/testing); labels outgoing alert emails
	AdminAlertEmail        string // BUGBARN_ADMIN_ALERT_EMAIL; per new issue/regression; defaults to BUGBARN_DIGEST_TO
	SelfEndpoint           string
	SelfAPIKey             string
	SelfProject            string
	Digest                 digest.Config
	AnalyticsRetentionDays int
	FunnelBarnEndpoint     string // BUGBARN_FUNNELBARN_ENDPOINT
	FunnelBarnAPIKey       string // BUGBARN_FUNNELBARN_API_KEY
	AutoApproveProjects    bool   // BUGBARN_AUTO_APPROVE_PROJECTS
	Mode                   string // BUGBARN_MODE: "", "writer", or "reader"
	WriterURL              string // BUGBARN_WRITER_URL: writer service URL (required when Mode=="reader")
	RedisQueueURL          string // BUGBARN_REDIS_QUEUE_URL: write-queue Redis URL; empty falls back to HTTP forwarding (spec 007)
	OIDCIssuer             string // BUGBARN_OIDC_ISSUER — when all four OIDC vars are set, OIDC login is offered alongside local auth
	OIDCClientID           string // BUGBARN_OIDC_CLIENT_ID
	OIDCClientSecret       string // BUGBARN_OIDC_CLIENT_SECRET
	OIDCRedirectURL        string // BUGBARN_OIDC_REDIRECT_URL
	OIDCRequiredGroup      string // BUGBARN_OIDC_REQUIRED_GROUP — defaults to "bugbarn-users"
}

// Load reads configuration from environment variables and config files.
func Load() Config {
	loadConfigFiles()

	cfg := Config{
		Addr:                   getenv("BUGBARN_ADDR", ":8080"),
		APIKey:                 os.Getenv("BUGBARN_API_KEY"),
		APIKeySHA256:           os.Getenv("BUGBARN_API_KEY_SHA256"),
		AdminUsername:          os.Getenv("BUGBARN_ADMIN_USERNAME"),
		AdminPassword:          os.Getenv("BUGBARN_ADMIN_PASSWORD"),
		AdminPasswordBcrypt:    os.Getenv("BUGBARN_ADMIN_PASSWORD_BCRYPT"),
		SessionSecret:          os.Getenv("BUGBARN_SESSION_SECRET"),
		AllowedOrigins:         parseCSVEnv("BUGBARN_ALLOWED_ORIGINS"),
		TrustedProxies:         parseTrustedProxies(os.Getenv("BUGBARN_TRUSTED_PROXIES")),
		SpoolDir:               getenv("BUGBARN_SPOOL_DIR", ".data/spool"),
		DBPath:                 getenv("BUGBARN_DB_PATH", ".data/bugbarn.db"),
		MaxBodyBytes:           envInt64Positive("BUGBARN_MAX_BODY_BYTES", 1<<20),
		MaxSpoolBytes:          envInt64Positive("BUGBARN_MAX_SPOOL_BYTES", 0),
		MaxSourceMapBytes:      envInt64Positive("BUGBARN_MAX_SOURCE_MAP_BYTES", 0),
		SessionTTL:             envDurationSeconds("BUGBARN_SESSION_TTL_SECONDS", 12*time.Hour),
		AnalyticsRetentionDays: envIntPositive("BUGBARN_ANALYTICS_RETENTION_DAYS", 90),
		PublicURL:              os.Getenv("BUGBARN_PUBLIC_URL"),
		Environment:            os.Getenv("BUGBARN_ENV"),
		SelfEndpoint:           os.Getenv("BUGBARN_SELF_ENDPOINT"),
		SelfAPIKey:             os.Getenv("BUGBARN_SELF_API_KEY"),
		SelfProject:            os.Getenv("BUGBARN_SELF_PROJECT"),
		FunnelBarnEndpoint:     os.Getenv("BUGBARN_FUNNELBARN_ENDPOINT"),
		FunnelBarnAPIKey:       os.Getenv("BUGBARN_FUNNELBARN_API_KEY"),
		AutoApproveProjects:    strings.EqualFold(os.Getenv("BUGBARN_AUTO_APPROVE_PROJECTS"), "true"),
		Mode:                   os.Getenv("BUGBARN_MODE"),
		WriterURL:              os.Getenv("BUGBARN_WRITER_URL"),
		RedisQueueURL:          os.Getenv("BUGBARN_REDIS_QUEUE_URL"),
		OIDCIssuer:             os.Getenv("BUGBARN_OIDC_ISSUER"),
		OIDCClientID:           os.Getenv("BUGBARN_OIDC_CLIENT_ID"),
		OIDCClientSecret:       os.Getenv("BUGBARN_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:        os.Getenv("BUGBARN_OIDC_REDIRECT_URL"),
		OIDCRequiredGroup:      getenv("BUGBARN_OIDC_REQUIRED_GROUP", "bugbarn-users"),
	}

	cfg.Digest = parseDigestConfig(cfg.PublicURL)

	// Global admin alert recipient. Every new issue and regression across all
	// projects is emailed here. Falls back to the weekly-digest recipient so a
	// single SMTP setup covers both without extra config.
	cfg.AdminAlertEmail = os.Getenv("BUGBARN_ADMIN_ALERT_EMAIL")
	if cfg.AdminAlertEmail == "" {
		cfg.AdminAlertEmail = cfg.Digest.Mail.To
	}

	validateMode(cfg)
	return cfg
}

// parseDigestConfig builds the weekly-digest configuration from env. SMTP vars
// use the same unprefixed names as rapid-root; BUGBARN_DIGEST_ENABLED toggles
// email independent of credentials.
func parseDigestConfig(publicURL string) digest.Config {
	return digest.Config{
		Day:        envIntInRange("BUGBARN_DIGEST_DAY", 0, 0, 6),
		Hour:       envIntInRange("BUGBARN_DIGEST_HOUR", 8, 0, 23),
		WebhookURL: os.Getenv("BUGBARN_DIGEST_WEBHOOK_URL"),
		PublicURL:  publicURL,
		Mail: digest.MailConfig{
			Enabled: os.Getenv("BUGBARN_DIGEST_ENABLED") == "true",
			Host:    os.Getenv("SMTP_HOST"),
			Port:    envIntPositive("SMTP_PORT", 587),
			User:    os.Getenv("SMTP_USER"),
			Pass:    os.Getenv("SMTP_PASS"),
			From:    os.Getenv("SMTP_FROM"),
			To:      os.Getenv("BUGBARN_DIGEST_TO"),
		},
	}
}

// validateMode enforces the allowed CQRS modes; it aborts the process on a
// misconfiguration, matching the original fail-fast behavior at startup.
func validateMode(cfg Config) {
	switch cfg.Mode {
	case "", "writer", "reader":
	default:
		log.Fatalf("invalid BUGBARN_MODE %q: must be \"\", \"writer\", or \"reader\"", cfg.Mode)
	}
	if cfg.Mode == "reader" && cfg.WriterURL == "" {
		log.Fatalf("BUGBARN_WRITER_URL is required when BUGBARN_MODE is \"reader\"")
	}
}

// parseCSVEnv splits a comma-separated env var into trimmed, non-empty values.
func parseCSVEnv(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// parseTrustedProxies parses a comma-separated list of CIDRs (a bare IP is
// treated as /32), skipping unparseable entries.
func parseTrustedProxies(raw string) []*net.IPNet {
	if raw == "" {
		return nil
	}
	var out []*net.IPNet
	for _, cidr := range strings.Split(raw, ",") {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if !strings.Contains(cidr, "/") {
			cidr += "/32"
		}
		if _, network, err := net.ParseCIDR(cidr); err == nil {
			out = append(out, network)
		}
	}
	return out
}

// envInt64Positive returns the parsed positive int64 at key, or fallback.
func envInt64Positive(key string, fallback int64) int64 {
	if raw := os.Getenv(key); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

// envIntPositive returns the parsed positive int at key, or fallback.
func envIntPositive(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

// envIntInRange returns the parsed int at key when within [lo,hi], else fallback.
func envIntInRange(key string, fallback, lo, hi int) int {
	if raw := os.Getenv(key); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= lo && parsed <= hi {
			return parsed
		}
	}
	return fallback
}

// envDurationSeconds returns a duration from a positive seconds value at key, or
// fallback.
func envDurationSeconds(key string, fallback time.Duration) time.Duration {
	if s := envInt64Positive(key, 0); s > 0 {
		return time.Duration(s) * time.Second
	}
	return fallback
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// loadConfigFiles applies KEY=VALUE config files to the process environment.
// Files are read in order: system-wide first, then user-specific. Values from
// later files win over earlier ones, but env vars already set in the environment
// always take precedence over values in any file.
//
// Supported locations:
//   - /etc/bugbarn/bugbarn.conf          (Linux system-wide, read by systemd EnvironmentFile)
//   - ~/.config/bugbarn/bugbarn.conf     (XDG user config, Linux + macOS)
func loadConfigFiles() {
	candidates := []string{
		"/etc/bugbarn/bugbarn.conf",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "bugbarn", "bugbarn.conf"))
	}
	for _, path := range candidates {
		if err := applyConfigFile(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("error reading config file", "path", path, "error", err)
		}
	}
}

// applyConfigFile reads KEY=VALUE pairs and sets them as environment variables
// for keys not already set. Blank lines and # comments are ignored.
// Values may optionally be wrapped in single or double quotes.
func applyConfigFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val) //nolint:errcheck
		}
	}
	return scanner.Err()
}
