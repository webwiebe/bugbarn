package api

import (
	"net/http"
	"strings"
)

func (s *Server) serveProjectsRoot(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		projects, err := s.store.ListProjects(r.Context())
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"projects": projects})
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, "invalid project payload", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if req.Slug == "" {
			req.Slug = slugify(req.Name)
		}
		p, err := s.store.CreateProject(r.Context(), req.Name, req.Slug)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, map[string]any{"project": p})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
