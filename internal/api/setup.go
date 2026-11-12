package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (s *Server) setupKey(slug string) string {
	if s.sessionSecret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(s.sessionSecret))
	mac.Write([]byte("setup:" + slug))
	return hex.EncodeToString(mac.Sum(nil))[:40]
}

// resolveSetupProject looks up the project for a setup request and enforces the
// active-project gate: brand-new/pending slugs onboard freely, but an existing
// active project's deterministic key requires an admin session (else an anonymous
// caller could forge events into an established stream). ok is false when a
// response was already written.
func (s *Server) resolveSetupProject(w http.ResponseWriter, r *http.Request, slug string) (domain.Project, bool) {
	proj, err := s.projects.BySlug(r.Context(), slug)
	if err != nil {
		// Unknown slug: legitimate self-service onboarding — the project is created
		// lazily, pending admin approval, on the writer when the first event arrives.
		proj.Slug = slug
		proj.Status = "new"
		return proj, true
	}
	if projectIsActive(proj.Status) && s.users != nil && s.users.Enabled() {
		if _, ok := s.sessionUser(r); !ok {
			http.Error(w, "setup key for an active project requires an authenticated admin session; "+
				"agents can ask an admin for a key: POST /api/v1/setup/"+slug+"/key-requests", http.StatusForbidden)
			return proj, false
		}
	}
	return proj, true
}

// projectIsActive reports whether a project is past the pending-approval stage.
// An empty status is treated as active, mirroring the display default below.
func projectIsActive(status string) bool {
	switch status {
	case "pending", "new":
		return false
	default:
		return true
	}
}

func (s *Server) serveSetup(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/v1/setup/")
	slug = strings.TrimSuffix(slug, "/")
	if slug == "" || strings.Contains(slug, "/") {
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	}

	// Rate-limit per IP: the setup page hands out a working ingest key, so cap how
	// fast an anonymous caller can mint keys / spawn pending projects.
	ip := s.clientIP(r)
	if s.overRateLimit(&s.setupLimiter, ip, setupRateLimit) {
		s.logger.Warn("setup: rate-limited", "ip", ip, "slug", slug)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many setup requests", http.StatusTooManyRequests)
		return
	}

	rawKey := s.setupKey(slug)
	if rawKey == "" {
		http.Error(w, "setup not configured (BUGBARN_SESSION_SECRET required)", http.StatusServiceUnavailable)
		return
	}

	proj, ok := s.resolveSetupProject(w, r, slug)
	if !ok {
		return
	}

	keySHA := hex.EncodeToString(sha256Sum(rawKey))
	if proj.ID != 0 {
		if err := s.projects.EnsureSetupAPIKey(r.Context(), slug+"-setup", proj.ID, keySHA); err != nil {
			s.logger.Error("setup: failed to provision setup api key", "slug", slug, "project_id", proj.ID, "error", err)
		}
	}

	endpoint, dashboard := s.setupEndpoints(r)

	status := proj.Status
	if status == "" {
		status = "active"
	}

	viewSection := dashboardSuffix(dashboard, "\n## View your project\n\n%s/#/issues\n", "")
	viewSection += keyRequestDocs(endpoint, slug)

	page := setupPage{
		slug: slug, endpoint: endpoint, rawKey: rawKey, status: status,
		pendingNote: pendingNote(status, dashboard), viewSection: viewSection,
	}.render(time.Now().UTC())

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = fmt.Fprint(w, page)
}

// pendingNote is the block that tells an integrator their events are queued until
// an admin approves a new project; empty for an active one.
func pendingNote(status, dashboard string) string {
	if status != "pending" && status != "new" {
		return ""
	}
	return fmt.Sprintf(`
## Pending admin approval

New projects are created pending approval. Send events now — they are accepted
(HTTP 202) and queued. They are ingested into the dashboard once an admin
approves the project%s
`, dashboardSuffix(dashboard, " at:\n  %s", "."))
}

// setupEndpoints returns the ingest endpoint the setup page documents and the
// dashboard URL it links to. A request on a bb.<domain> vanity alias documents
// that alias, so an integrator only ever sees the host they were given; the
// alias serves ingest paths only, so the dashboard URL is empty there.
// Otherwise BUGBARN_PUBLIC_URL wins, falling back to the request host.
func (s *Server) setupEndpoints(r *http.Request) (endpoint, dashboard string) {
	host, scheme := s.requestHost(r)
	if vanity, ok := vanityIngestHost(host); ok {
		return "https://" + vanity, ""
	}
	if s.publicURL != "" {
		base := strings.TrimRight(s.publicURL, "/")
		return base, base
	}
	base := scheme + "://" + host
	return base, base
}

// requestHost returns the host and scheme the client used. X-Forwarded-Host and
// X-Forwarded-Proto are only honored from a trusted proxy, like clientIP.
func (s *Server) requestHost(r *http.Request) (host, scheme string) {
	host, scheme = r.Host, "https"
	if len(s.trustedProxies) > 0 && isTrustedProxy(remoteHost(r.RemoteAddr), s.trustedProxies) {
		if fh := firstHeaderValue(r.Header.Get("X-Forwarded-Host")); fh != "" {
			host = fh
		}
		if fp := firstHeaderValue(r.Header.Get("X-Forwarded-Proto")); fp == "http" || fp == "https" {
			scheme = fp
		}
	}
	return host, scheme
}

func firstHeaderValue(v string) string {
	if i := strings.Index(v, ","); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

// vanityIngestHost returns host without its port when it is a bb.<domain>
// alias, the pattern the production IngressRoute bugbarn-forward-domains routes
// to BugBarn. The alias is always served over HTTPS on the default port.
func vanityIngestHost(host string) (string, bool) {
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host, strings.HasPrefix(host, "bb.") && len(host) > len("bb.")
}

// dashboardSuffix formats withURL around the dashboard URL, or returns without
// when there is no dashboard to link to.
func dashboardSuffix(dashboard, withURL, without string) string {
	if dashboard == "" {
		return without
	}
	return fmt.Sprintf(withURL, dashboard)
}

func sha256Sum(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}
