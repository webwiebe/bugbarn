package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// authenticateAndResolve enforces authentication, API-key scope, and CSRF for
// the protected endpoints, then resolves the request's project/group scope into
// its context. It returns the (possibly context-augmented) request and true to
// proceed, or false after writing an error response. When user auth is not
// configured it is a pass-through.
//
//nolint:gocognit,gocyclo,funlen // auth + key-scope + CSRF + project/group resolution pipeline; tracked for refactor.
func (s *Server) authenticateAndResolve(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	// All remaining endpoints require authentication. When auth is configured, the
	// request must carry either a valid session cookie or a valid API key. Full-scope
	// API keys are accepted on all protected endpoints; ingest-only keys are rejected here
	// (they may only reach the ingest endpoint handled above).
	var resolvedProjectID int64
	if s.users == nil || !s.users.Enabled() {
		return r, true
	}

	_, usingSession := s.sessionUser(r)
	var apiKeyProjectID int64
	usingAPIKey := false
	if s.ingestHandler != nil {
		pid, scope, ok := s.ingestHandler.APIKeyProjectScope(r)
		if ok {
			if scope == domain.APIKeyScopeIngest {
				// Allow ingest keys to post release markers — the setup page uses
				// the same key for both events and releases — but reject every
				// other endpoint.
				isReleaseMarker := r.URL.Path == "/api/v1/releases" && r.Method == http.MethodPost
				if !isReleaseMarker {
					s.logger.Warn("auth: ingest-only key rejected", "method", r.Method, "path", r.URL.Path)
					http.Error(w, "forbidden: ingest-only key cannot access this endpoint", http.StatusForbidden)
					return r, false
				}
			}
			if scope == domain.APIKeyScopeRead {
				if r.Method != http.MethodGet {
					s.logger.Warn("auth: read-only key rejected non-GET", "method", r.Method, "path", r.URL.Path)
					http.Error(w, "forbidden: read-only key cannot modify data", http.StatusForbidden)
					return r, false
				}
				if strings.HasPrefix(r.URL.Path, "/api/v1/settings") || strings.HasPrefix(r.URL.Path, "/api/v1/apikeys") {
					s.logger.Warn("auth: read-only key rejected settings/apikeys", "method", r.Method, "path", r.URL.Path)
					http.Error(w, "forbidden: read-only key cannot access this endpoint", http.StatusForbidden)
					return r, false
				}
			}
			usingAPIKey = true
			apiKeyProjectID = pid
		}
	}

	if !usingSession && !usingAPIKey {
		s.logger.Warn("auth: rejected request", "method", r.Method, "path", r.URL.Path, "reason", "no session or API key")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return r, false
	}

	// CSRF protection: only applies to session-authenticated state-changing requests.
	// API key requests are authenticated out-of-band, so CSRF doesn't apply.
	if usingSession && !usingAPIKey && isCSRFProtected(r) {
		if !s.validCSRF(r) {
			s.logger.Warn("csrf: rejected request", "method", r.Method, "path", r.URL.Path, "header_present", r.Header.Get("X-BugBarn-CSRF") != "")
			http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
			return r, false
		}
	}

	// Refresh the CSRF cookie on every session-authenticated response when
	// the browser's copy is missing or stale, so the frontend always has a
	// valid token available for state-changing requests.
	if usingSession && s.sessions != nil {
		if sessionCookie, err := r.Cookie("bugbarn_session"); err == nil {
			if csrfCookie, err := r.Cookie("bugbarn_csrf"); err != nil || csrfCookie.Value != s.sessions.CSRFToken(sessionCookie.Value) {
				secure := secureCookie(r)
				expires := time.Now().Add(12 * time.Hour)
				http.SetCookie(w, s.sessions.CSRFCookie(sessionCookie.Value, expires, secure))
			}
		}
	}

	// Resolve project ID. X-BugBarn-Project header takes precedence for both
	// API key and session requests — it auto-creates the project if unknown,
	// so a single shared API key can route events to any project via the header.
	if slug := r.Header.Get("X-BugBarn-Project"); slug != "" {
		if proj, err := s.projects.Ensure(r.Context(), slug); err == nil {
			resolvedProjectID = proj.ID
		}
	} else if usingAPIKey && apiKeyProjectID > 0 {
		resolvedProjectID = apiKeyProjectID
	} else if usingSession && r.Method != http.MethodGet {
		if !isIssueAction(r) {
			if proj, err := s.projects.BySlug(r.Context(), "default"); err == nil {
				resolvedProjectID = proj.ID
			}
		}
	}
	if resolvedProjectID == 0 && (!usingSession || r.Method != http.MethodGet) {
		resolvedProjectID = s.projects.DefaultProjectID()
	}
	r = r.WithContext(storage.WithProjectID(r.Context(), resolvedProjectID))

	// Group-scoped filtering: when a GET carries X-BugBarn-Group and no
	// specific project was selected, resolve the group to its member IDs so that
	// storage queries use IN (...) instead of showing all projects.
	// Works for both session and API key auth.
	if r.Method == http.MethodGet && resolvedProjectID == 0 {
		if groupSlug := r.Header.Get("X-BugBarn-Group"); groupSlug != "" {
			if members, err := s.projects.ListGroupProjects(r.Context(), groupSlug); err == nil && len(members) > 0 {
				ids := make([]int64, len(members))
				for i, p := range members {
					ids[i] = p.ID
				}
				r = r.WithContext(storage.WithProjectIDs(r.Context(), ids))
			}
		}
	}

	return r, true
}
