package api

import (
	"net/http"
	"strings"
)

func (s *Server) approveProject(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/approve")
	if err := s.projects.Approve(r.Context(), slug); err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) servePendingProjectCount(w http.ResponseWriter, r *http.Request) {
	projects, err := s.projects.List(r.Context())
	if err != nil {
		writeStorageError(w, err)
		return
	}
	var pending []string
	for _, p := range projects {
		if p.Status == "pending" {
			pending = append(pending, p.Slug)
		}
	}
	writeJSON(w, map[string]any{"count": len(pending), "slugs": pending})
}

func (s *Server) serveProjectsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projects, err := s.projects.List(r.Context())
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
		p, err := s.projects.Create(r.Context(), req.Name, req.Slug)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, map[string]any{"project": p})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
