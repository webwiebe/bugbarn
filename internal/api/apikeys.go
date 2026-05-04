package api

import (
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
	writeJSON(w, map[string]any{"apiKeys": out})
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/apikeys/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid api key id", http.StatusBadRequest)
		return
	}
	if err := s.projects.DeleteAPIKey(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"deleted": true})
}
