package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Cloudflare configures the Cloudflare firewall-event poller (internal/cfpoller).
type Cloudflare struct {
	// APIToken is BUGBARN_CLOUDFLARE_API_TOKEN, a secret. It needs
	// Zone > Analytics > Read on every zone in ZoneIDs.
	APIToken string
	// ZoneIDs is BUGBARN_CLOUDFLARE_ZONE_IDS, comma-separated zone tags.
	ZoneIDs []string
	// PollInterval is BUGBARN_CLOUDFLARE_POLL_INTERVAL: a Go duration ("60s",
	// "2m") or whole seconds. Default 60s, minimum 10s.
	PollInterval time.Duration
}

// Enabled reports whether the poller should run: it needs a token and at
// least one zone.
func (c Cloudflare) Enabled() bool { return c.APIToken != "" && len(c.ZoneIDs) > 0 }

const (
	defaultCloudflarePollInterval = 60 * time.Second
	minCloudflarePollInterval     = 10 * time.Second
)

func parseCloudflareConfig() Cloudflare {
	return Cloudflare{
		APIToken:     strings.TrimSpace(os.Getenv("BUGBARN_CLOUDFLARE_API_TOKEN")),
		ZoneIDs:      parseCSVEnv("BUGBARN_CLOUDFLARE_ZONE_IDS"),
		PollInterval: parsePollInterval(os.Getenv("BUGBARN_CLOUDFLARE_POLL_INTERVAL")),
	}
}

func parsePollInterval(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultCloudflarePollInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		s, serr := strconv.Atoi(raw)
		if serr != nil {
			return defaultCloudflarePollInterval
		}
		d = time.Duration(s) * time.Second
	}
	if d < minCloudflarePollInterval {
		return defaultCloudflarePollInterval
	}
	return d
}
