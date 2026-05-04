package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (s *Server) serveReleasesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		releases, err := s.releases.List(r.Context())
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"releases": releases})
	case http.MethodPost:
		var request struct {
			Name        string `json:"name"`
			Environment string `json:"environment"`
			ObservedAt  string `json:"observedAt"`
			Version     string `json:"version"`
			CommitSHA   string `json:"commitSha"`
			URL         string `json:"url"`
			Notes       string `json:"notes"`
			CreatedBy   string `json:"createdBy"`
		}
		if err := decodeJSON(w, r, &request); err != nil {
			http.Error(w, "invalid release payload", http.StatusBadRequest)
			return
		}
		release := domain.Release{
			Name:        request.Name,
			Environment: request.Environment,
			Version:     request.Version,
			CommitSHA:   request.CommitSHA,
			URL:         request.URL,
			Notes:       request.Notes,
			CreatedBy:   request.CreatedBy,
		}
		if parsed, err := time.Parse(time.RFC3339Nano, request.ObservedAt); err == nil {
			release.ObservedAt = parsed
		}
		item, err := s.releases.Create(r.Context(), release)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"release": item})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveReleaseRoute(w http.ResponseWriter, r *http.Request) {
	releaseID := strings.TrimPrefix(r.URL.Path, "/api/v1/releases/")
	switch r.Method {
	case http.MethodGet:
		item, err := s.releases.Get(r.Context(), releaseID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"release": item})
	case http.MethodPut:
		var request struct {
			Name        string `json:"name"`
			Environment string `json:"environment"`
			ObservedAt  string `json:"observedAt"`
			Version     string `json:"version"`
			CommitSHA   string `json:"commitSha"`
			URL         string `json:"url"`
			Notes       string `json:"notes"`
			CreatedBy   string `json:"createdBy"`
		}
		if err := decodeJSON(w, r, &request); err != nil {
			http.Error(w, "invalid release payload", http.StatusBadRequest)
			return
		}
		release := domain.Release{
			Name:        request.Name,
			Environment: request.Environment,
			Version:     request.Version,
			CommitSHA:   request.CommitSHA,
			URL:         request.URL,
			Notes:       request.Notes,
			CreatedBy:   request.CreatedBy,
		}
		if parsed, err := time.Parse(time.RFC3339Nano, request.ObservedAt); err == nil {
			release.ObservedAt = parsed
		}
		item, err := s.releases.Update(r.Context(), releaseID, release)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"release": item})
	case http.MethodDelete:
		if err := s.releases.Delete(r.Context(), releaseID); err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
