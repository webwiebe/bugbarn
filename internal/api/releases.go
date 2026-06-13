package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func (s *Server) serveReleasesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		releases, err := s.releases.List(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"releases": releases})
	case http.MethodPost:
		// Fire-and-forget: when an ingest spool/worker is wired, enqueue the
		// release marker and return immediately. Creating it synchronously here
		// would grab the single SQLite writer connection that the background
		// worker also holds while persisting events, so under load the create
		// blocks for many seconds and frequently times out. Auth was already
		// validated upstream in ServeHTTP. The worker creates the release within
		// one tick (~1s). When no ingest handler is configured (e.g. unit tests
		// or a writer without a worker), fall back to a synchronous create so the
		// release is immediately listable.
		if s.ingestHandler != nil {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.ingestHandler.MaxBodyBytes()))
			if err != nil {
				http.Error(w, "invalid release payload", http.StatusBadRequest)
				return
			}
			projectID, _ := storage.ProjectIDFromContext(r.Context())
			ingestID, err := s.ingestHandler.SpoolRelease(projectID, r.Header.Get("Content-Type"), r.RemoteAddr, body)
			if err != nil {
				if errors.Is(err, spool.ErrFull) {
					w.Header().Set("Retry-After", "1")
					http.Error(w, "ingest spool full", http.StatusTooManyRequests)
					return
				}
				http.Error(w, "release enqueue failed", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted": true, "ingestId": ingestID})
			return
		}

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
			writeServiceError(w, err)
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
			writeServiceError(w, err)
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
			writeServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"release": item})
	case http.MethodDelete:
		if err := s.releases.Delete(r.Context(), releaseID); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
