package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.projects.ListAPIKeys(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	type safeKey struct {
		ID         int64     `json:"id"`
		Name       string    `json:"name"`
		ProjectID  int64     `json:"projectId"`
		Scope      string    `json:"scope"`
		CreatedAt  time.Time `json:"createdAt"`
		LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
	}
	out := make([]safeKey, 0, len(keys))
	for _, k := range keys {
		scope := k.Scope
		if scope == "" {
			scope = domain.APIKeyScopeFull
		}
		out = append(out, safeKey{
			ID:         k.ID,
			Name:       k.Name,
			ProjectID:  k.ProjectID,
			Scope:      scope,
			CreatedAt:  k.CreatedAt,
			LastUsedAt: k.LastUsedAt,
		})
	}
	revoked, err := s.projects.ListSetupKeyRevocations(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if revoked == nil {
		revoked = []domain.SetupKeyRevocation{}
	}
	writeJSON(w, map[string]any{"apiKeys": out, "revokedSetupKeys": revoked})
}

// deleteAPIKey removes a key. A setup key is derived, so removing only its row
// would leave it working through the setup fallback: it gets revoked instead.
func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/apikeys/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid api key id", http.StatusBadRequest)
		return
	}
	key, err := s.projects.APIKey(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if slug, ok := s.setupKeySlug(key); ok {
		err = s.projects.RevokeSetupKey(r.Context(), slug, key.KeySHA256)
	} else {
		err = s.projects.DeleteAPIKey(r.Context(), id)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"deleted": true})
}

// setupKeySlug returns the slug of a row that holds a project's derived setup key.
// The hash decides: a key that is only named like one is an ordinary key.
func (s *Server) setupKeySlug(key domain.APIKey) (string, bool) {
	slug, named := strings.CutSuffix(key.Name, "-setup")
	if !named {
		return "", false
	}
	derived := s.setupKey(slug)
	if derived == "" || hex.EncodeToString(sha256Sum(derived)) != key.KeySHA256 {
		return "", false
	}
	return slug, true
}

// revokeSetupKey ends the setup key of a slug that may have no api_keys row. It
// takes a signed-in admin, like approving a key request.
func (s *Server) revokeSetupKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessionUser(r); !ok {
		s.logger.Warn("auth: setup key revocation without a session", "method", r.Method, "path", r.URL.Path)
		http.Error(w, "forbidden: revoking a setup key requires an admin session", http.StatusForbidden)
		return
	}
	var body struct {
		Slug string `json:"slug"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(body.Slug)
	if slug == "" || strings.Contains(slug, "/") {
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	}
	derived := s.setupKey(slug)
	if derived == "" {
		http.Error(w, "setup not configured (BUGBARN_SESSION_SECRET required)", http.StatusServiceUnavailable)
		return
	}
	if err := s.projects.RevokeSetupKey(r.Context(), slug, hex.EncodeToString(sha256Sum(derived))); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"revoked": true})
}
