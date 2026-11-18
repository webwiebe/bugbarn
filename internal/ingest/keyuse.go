package ingest

import (
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

const (
	// keyUseLogEvery bounds how often one key and user agent pair is logged.
	keyUseLogEvery = time.Hour
	// keyUseMaxCallers bounds the throttle table; it resets when full.
	keyUseMaxCallers = 1000
	// maxUserAgentLen caps the user agent on spans and log lines.
	maxUserAgentLen = 120
)

// keyUseCaller is one key used by one user agent.
type keyUseCaller struct {
	keyID int64
	ua    string
}

// SetClientIP sets how the handler names a caller's address in the key-use log
// line. The API server passes its trusted-proxy aware resolver; without one the
// handler uses the connection's remote address.
func (h *Handler) SetClientIP(fn func(*http.Request) string) {
	if h != nil {
		h.clientIP = fn
	}
}

// observeKeyUse records which key authenticated a request, so a leaked key can
// be traced to its callers. The span carries the key id and user agent on every
// request. A log line carries them once per key and user agent per hour, which
// shows a rarely used key. The key and its hash never reach a span or a log;
// the id is the api_keys row id, and 0 for the static key and for setup keys.
func (h *Handler) observeKeyUse(r *http.Request, id auth.KeyIdentity) {
	ua := r.Header.Get("User-Agent")
	if len(ua) > maxUserAgentLen {
		ua = ua[:maxUserAgentLen]
	}
	if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
		span.SetAttributes(
			attribute.Int64("bugbarn.caller.api_key_id", id.ID),
			attribute.String("bugbarn.caller.user_agent", ua),
		)
	}

	now := h.now()
	caller := keyUseCaller{keyID: id.ID, ua: ua}
	h.keyUseMu.Lock()
	if len(h.keyUseAt) >= keyUseMaxCallers {
		h.keyUseAt = map[keyUseCaller]time.Time{}
	}
	last, seen := h.keyUseAt[caller]
	throttled := seen && now.Sub(last) < keyUseLogEvery
	if !throttled {
		h.keyUseAt[caller] = now
	}
	h.keyUseMu.Unlock()
	if throttled {
		return
	}

	h.logger.InfoContext(r.Context(), "api key used",
		"key_id", id.ID, "project_id", id.ProjectID, "scope", id.Scope,
		"ua", ua, "ip", h.callerIP(r), "path", r.URL.Path)
}

func (h *Handler) callerIP(r *http.Request) string {
	if h.clientIP != nil {
		return h.clientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
