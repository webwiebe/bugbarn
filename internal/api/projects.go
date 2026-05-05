package api

import (
	"net/http"
	"strings"
)

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/v1/projects/")
	if err := s.projects.Delete(r.Context(), slug); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) approveProject(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/approve")
	if err := s.projects.Approve(r.Context(), slug); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) servePendingProjectCount(w http.ResponseWriter, r *http.Request) {
	projects, err := s.projects.List(r.Context())
	if err != nil {
		writeServiceError(w, err)
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
			writeServiceError(w, err)
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
			writeServiceError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, map[string]any{"project": p})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renameProject handles PUT /api/v1/projects/:slug
func (s *Server) renameProject(w http.ResponseWriter, r *http.Request) {
	oldSlug := strings.TrimPrefix(r.URL.Path, "/api/v1/projects/")
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Name == "" || req.Slug == "" {
		http.Error(w, "name and slug are required", http.StatusBadRequest)
		return
	}
	if err := s.projects.Rename(r.Context(), oldSlug, req.Slug, req.Name); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// mergeProject handles POST /api/v1/projects/:slug/merge
func (s *Server) mergeProject(w http.ResponseWriter, r *http.Request) {
	sourceSlug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/merge")
	var req struct {
		Target string `json:"target"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	req.Target = strings.TrimSpace(req.Target)
	if req.Target == "" {
		http.Error(w, "target is required", http.StatusBadRequest)
		return
	}
	if err := s.projects.Merge(r.Context(), sourceSlug, req.Target); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// --- Group endpoints ---

func (s *Server) serveGroupsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		groups, err := s.projects.ListGroups(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"groups": groups})
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
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
		g, err := s.projects.CreateGroup(r.Context(), req.Name, req.Slug)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, map[string]any{"group": g})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/v1/groups/")
	if err := s.projects.DeleteGroup(r.Context(), slug); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"deleted": true})
}

func (s *Server) assignProjectToGroup(w http.ResponseWriter, r *http.Request) {
	// Path: /api/v1/groups/:slug/projects
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/groups/")
	groupSlug := strings.TrimSuffix(path, "/projects")
	var req struct {
		Project string `json:"project"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	req.Project = strings.TrimSpace(req.Project)
	if req.Project == "" {
		http.Error(w, "project is required", http.StatusBadRequest)
		return
	}
	if err := s.projects.AssignProjectToGroup(r.Context(), req.Project, groupSlug); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) removeProjectFromGroup(w http.ResponseWriter, r *http.Request) {
	// Path: /api/v1/groups/:slug/projects/:project
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/groups/")
	parts := strings.Split(path, "/projects/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	projectSlug := parts[1]
	if err := s.projects.RemoveProjectFromGroup(r.Context(), projectSlug); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) serveGroupRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/groups/")

	// DELETE /api/v1/groups/:slug/projects/:project
	if strings.Contains(path, "/projects/") && r.Method == http.MethodDelete {
		s.removeProjectFromGroup(w, r)
		return
	}

	// POST /api/v1/groups/:slug/projects
	if strings.HasSuffix(path, "/projects") && r.Method == http.MethodPost {
		s.assignProjectToGroup(w, r)
		return
	}

	// DELETE /api/v1/groups/:slug
	if r.Method == http.MethodDelete && !strings.Contains(path, "/") {
		s.deleteGroup(w, r)
		return
	}

	http.NotFound(w, r)
}
