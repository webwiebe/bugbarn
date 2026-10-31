package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// pollSecretHeader carries the one-time poll secret. A header keeps it out of
// access logs and URLs.
const pollSecretHeader = "X-BugBarn-Poll-Secret"

// serveKeyRequestEndpoint handles the public side of agent key requests:
//
//	POST /api/v1/setup/<slug>/key-requests        open a request
//	GET  /api/v1/setup/<slug>/key-requests/<id>   poll it (claims the key once approved)
//
// Both write (the poll claims the key), so a reader forwards them to the writer.
// It returns true when the path belongs to this endpoint.
func (s *Server) serveKeyRequestEndpoint(w http.ResponseWriter, r *http.Request) bool {
	slug, id, ok := parseKeyRequestPath(r.URL.Path)
	if !ok {
		return false
	}
	create := id == "" && r.Method == http.MethodPost
	poll := id != "" && r.Method == http.MethodGet
	if !create && !poll {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}

	ip := s.clientIP(r)
	if s.overRateLimit(&s.setupLimiter, ip, setupRateLimit) {
		s.logger.Warn("setup: key request rate-limited", "ip", ip, "slug", slug)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many setup requests", http.StatusTooManyRequests)
		return true
	}
	if s.writeForwarder != nil {
		s.writeForwarder.Forward(w, r)
		return true
	}
	if s.keyRequests == nil {
		http.Error(w, "key requests are not available", http.StatusServiceUnavailable)
		return true
	}

	if create {
		s.createKeyRequest(w, r, slug)
	} else {
		s.pollKeyRequest(w, r, slug, id)
	}
	return true
}

// parseKeyRequestPath splits /api/v1/setup/<slug>/key-requests[/<id>]. The id is
// empty for the collection path.
func parseKeyRequestPath(path string) (slug, id string, ok bool) {
	rest, ok := strings.CutPrefix(path, "/api/v1/setup/")
	if !ok {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || parts[1] != "key-requests" {
		return "", "", false
	}
	if len(parts) == 3 {
		id = parts[2]
	}
	return parts[0], id, true
}

func (s *Server) createKeyRequest(w http.ResponseWriter, r *http.Request, slug string) {
	proj, err := s.projects.BySlug(r.Context(), slug)
	// Only an existing, active project can get a key this way. New slugs
	// onboard through the setup page and need no request.
	if err != nil || !projectIsActive(proj.Status) {
		http.Error(w, "unknown or inactive project: use GET /api/v1/setup/"+slug+" for a new project", http.StatusNotFound)
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	created, err := s.keyRequests.Create(r.Context(), proj, body.Label)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]any{
		"id":         created.ID,
		"pollSecret": created.PollSecret,
		"status":     domain.KeyRequestPending,
		"expiresAt":  created.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) pollKeyRequest(w http.ResponseWriter, r *http.Request, slug, id string) {
	secret := r.Header.Get(pollSecretHeader)
	if secret == "" {
		http.Error(w, "missing "+pollSecretHeader, http.StatusUnauthorized)
		return
	}
	res, err := s.keyRequests.Poll(r.Context(), slug, id, secret)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if res.Status == domain.KeyRequestExpired {
		writeJSONStatus(w, http.StatusGone, map[string]any{"status": res.Status})
		return
	}
	out := map[string]any{"status": res.Status}
	if res.APIKey != "" {
		out["apiKey"] = res.APIKey
	}
	writeJSON(w, out)
}

// keyRequestDocs is the setup-page section for agents that need a key for an
// existing project (a rotated or lost key). It lives here, next to the endpoint
// it documents.
func keyRequestDocs(endpoint, slug string) string {
	return fmt.Sprintf(`
## Need a new key for an existing project?

An agent can ask for one; an admin approves it in the dashboard. The request
holds no key. The key is minted on the one poll that follows the approval.

~~~bash
# 1. ask (label = repo and environment, shown to the admin)
curl -s -X POST %[1]s/api/v1/setup/%[2]s/key-requests \
  -H "Content-Type: application/json" \
  -d '{"label": "my-org/my-repo production"}'
# -> {"id":"...","pollSecret":"...","status":"pending","expiresAt":"..."}

# 2. poll until an admin approves (requests expire after 24 hours)
curl -s %[1]s/api/v1/setup/%[2]s/key-requests/<id> \
  -H "X-BugBarn-Poll-Secret: <pollSecret>"
# -> {"status":"pending"} | {"status":"denied"} | HTTP 410 {"status":"expired"}
# -> {"status":"approved","apiKey":"..."} exactly once, then {"status":"claimed"}
~~~

Or with the CLI, which waits for the approval and writes the key to stdout only
when asked: `+"`bb key-request %[2]s --label my-org/my-repo --output-secret | gh secret set BUGBARN_API_KEY`"+`
The key is project-scoped and can post events and release markers.
`, endpoint, slug)
}

// dispatchKeyRequestRoutes serves the admin side: list the open requests and
// approve or deny one. Writes reach the writer through the generic reader forward.
func (s *Server) dispatchKeyRequestRoutes(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/v1/key-requests" && r.Method == http.MethodGet {
		s.listKeyRequests(w, r)
		return true
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/key-requests/")
	if !ok || r.Method != http.MethodPost {
		return false
	}
	id, action, found := strings.Cut(rest, "/")
	if !found || id == "" || (action != "approve" && action != "deny") {
		return false
	}
	s.resolveKeyRequest(w, r, id, action)
	return true
}

func (s *Server) listKeyRequests(w http.ResponseWriter, r *http.Request) {
	list, err := s.keyRequests.ListPending(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if bound, confined := boundProject(r.Context()); confined {
		own := make([]domain.KeyRequest, 0, len(list))
		for _, kr := range list {
			if kr.ProjectID == bound {
				own = append(own, kr)
			}
		}
		list = own
	}
	if list == nil {
		list = []domain.KeyRequest{}
	}
	writeJSON(w, map[string]any{"keyRequests": list})
}

func (s *Server) resolveKeyRequest(w http.ResponseWriter, r *http.Request, id, action string) {
	// Approving mints a new key, so it takes a signed-in admin. An API key of any
	// scope is refused here.
	actor, ok := s.sessionUser(r)
	if !ok {
		s.logger.Warn("auth: key request decision without a session", "method", r.Method, "path", r.URL.Path)
		http.Error(w, "forbidden: deciding a key request requires an admin session", http.StatusForbidden)
		return
	}
	var err error
	if action == "approve" {
		err = s.keyRequests.Approve(r.Context(), id, actor)
	} else {
		err = s.keyRequests.Deny(r.Context(), id, actor)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
