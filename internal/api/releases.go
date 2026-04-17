package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func (s *Server) serveReleasesRoot(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		releases, err := s.service.ListReleases(r.Context())
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
		release := storage.Release{
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
		item, err := s.service.CreateRelease(r.Context(), release)
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
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	releaseID := strings.TrimPrefix(r.URL.Path, "/api/v1/releases/")
	switch r.Method {
	case http.MethodGet:
		item, err := s.service.GetRelease(r.Context(), releaseID)
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
		release := storage.Release{
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
		item, err := s.service.UpdateRelease(r.Context(), releaseID, release)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"release": item})
	case http.MethodDelete:
		if err := s.service.DeleteRelease(r.Context(), releaseID); err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
