package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

const (
	// deniedLogEvery bounds how often one caller's denials are logged.
	deniedLogEvery = time.Minute
	// deniedLogMaxCallers bounds the throttle table; it resets when full.
	deniedLogMaxCallers = 1000
)

// keyFingerprint names a presented API key without exposing it: the first six
// hex characters of its SHA-256, or "missing" when the request carried none.
func keyFingerprint(key string) string {
	if key == "" {
		return "missing"
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:6]
}

// logDenied records a rejected API key at WARN so a burst of 401s can be traced
// to its sender: key fingerprint, user agent, project header and path. The key
// itself never reaches the log. Each caller is logged at most once per
// deniedLogEvery, so a flood of denials stays one line per minute per sender.
func (h *Handler) logDenied(r *http.Request) {
	key := keyFingerprint(r.Header.Get(auth.HeaderAPIKey))
	ua := r.Header.Get("User-Agent")
	project := r.Header.Get("x-bugbarn-project")
	if len(ua) > 120 {
		ua = ua[:120]
	}
	if len(project) > 64 {
		project = project[:64]
	}

	caller := key + "|" + ua + "|" + project
	now := h.now()
	h.deniedMu.Lock()
	if len(h.deniedAt) >= deniedLogMaxCallers {
		h.deniedAt = map[string]time.Time{}
	}
	last, seen := h.deniedAt[caller]
	throttled := seen && now.Sub(last) < deniedLogEvery
	if !throttled {
		h.deniedAt[caller] = now
	}
	h.deniedMu.Unlock()
	if throttled {
		return
	}

	h.logger.Warn("ingest api key denied", "key", key, "ua", ua, "project", project, "path", r.URL.Path)
}
