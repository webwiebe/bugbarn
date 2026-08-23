package api

import (
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
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

// projectWithUsage is one row of the project list. The three count fields are
// pointers so that "not asked for" and "asked for but unavailable" both render
// as an absent key, distinct from a real zero. They used to be plain ints
// defaulted from a discarded error, which meant a failed usage query was
// presented to the UI as "this project has no data".
type projectWithUsage struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Status       string `json:"status"`
	IssuePrefix  string `json:"issue_prefix"`
	IssueCounter int    `json:"issue_counter"`
	GroupID      *int64 `json:"group_id"`
	CreatedAt    string `json:"created_at"`
	IssueCount   *int   `json:"issue_count,omitempty"`
	EventCount   *int   `json:"event_count,omitempty"`
	LogCount     *int   `json:"log_count,omitempty"`
}

func (s *Server) serveProjectsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listProjects(w, r)
	case http.MethodPost:
		name, slug, ok := decodeNameAndSlug(w, r, "invalid project payload")
		if !ok {
			return
		}
		p, err := s.projects.Create(r.Context(), name, slug)
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
		name, slug, ok := decodeNameAndSlug(w, r, "invalid payload")
		if !ok {
			return
		}
		g, err := s.projects.CreateGroup(r.Context(), name, slug)
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

	// GET /api/v1/groups/:slug/projects
	if strings.HasSuffix(path, "/projects") && r.Method == http.MethodGet {
		groupSlug := strings.TrimSuffix(path, "/projects")
		projects, err := s.projects.ListGroupProjects(r.Context(), groupSlug)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"projects": projects})
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

// --- Alias endpoints ---

func (s *Server) serveAliasesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		aliases, err := s.projects.ListAliases(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if aliases == nil {
			aliases = []domain.ProjectAlias{}
		}
		writeJSON(w, map[string]any{"aliases": aliases})
	case http.MethodPost:
		var req struct {
			Alias   string `json:"alias"`
			Project string `json:"project"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		req.Alias = strings.TrimSpace(req.Alias)
		req.Project = strings.TrimSpace(req.Project)
		if req.Alias == "" || req.Project == "" {
			http.Error(w, "alias and project are required", http.StatusBadRequest)
			return
		}
		p, err := s.projects.BySlug(r.Context(), req.Project)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if err := s.projects.CreateAlias(r.Context(), req.Alias, p.ID); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) deleteAlias(w http.ResponseWriter, r *http.Request) {
	aliasSlug := strings.TrimPrefix(r.URL.Path, "/api/v1/aliases/")
	if err := s.projects.DeleteAlias(r.Context(), aliasSlug); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"deleted": true})
}

// listProjects handles GET /api/v1/projects.
//
// Usage counts are opt-in via ?usage=true. Computing them aggregates three of
// the largest tables with no WHERE clause, and only the settings screen renders
// them — but every caller used to pay for them, including the project picker
// that every dashboard load blocks on. In production that made the plain list
// take upward of two seconds and time out under its own weight.
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.projects.List(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}

	out := make([]projectWithUsage, len(projects))
	for i, p := range projects {
		out[i] = projectWithUsage{
			ID:           p.ID,
			Name:         p.Name,
			Slug:         p.Slug,
			Status:       p.Status,
			IssuePrefix:  p.IssuePrefix,
			IssueCounter: p.IssueCounter,
			GroupID:      p.GroupID,
			CreatedAt:    p.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	resp := map[string]any{"projects": out}
	if r.URL.Query().Get("usage") == "true" {
		s.attachUsage(r, projects, out, resp)
	}

	writeJSON(w, resp)
}

// attachUsage fills in the per-project counts, or records why it could not.
//
// A usage failure is reported, never papered over: the caller gets the project
// list it asked for with the counts absent and usage_available=false, so the UI
// can show "unavailable" instead of the zeros this endpoint used to invent when
// it discarded the error.
func (s *Server) attachUsage(r *http.Request, projects []domain.Project, out []projectWithUsage, resp map[string]any) {
	usage, stale, err := s.projects.UsageAll(r.Context())
	if err != nil {
		resp["usage_available"] = false
		return
	}
	resp["usage_available"] = true
	if stale {
		resp["usage_stale"] = true
	}
	for i, p := range projects {
		// A project with no rows in any counted table has no map entry; that is
		// a genuine zero, not missing data.
		u := usage[p.ID]
		issues, events, logs := u.IssueCount, u.EventCount, u.LogCount
		out[i].IssueCount = &issues
		out[i].EventCount = &events
		out[i].LogCount = &logs
	}
}

// decodeNameAndSlug reads the {name, slug} body shared by the create-project
// and create-group endpoints, defaulting the slug from the name. It writes the
// error response and returns false when the body is unusable.
func decodeNameAndSlug(w http.ResponseWriter, r *http.Request, badBodyMsg string) (name, slug string, ok bool) {
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, badBodyMsg, http.StatusBadRequest)
		return "", "", false
	}
	name = strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return "", "", false
	}
	slug = req.Slug
	if slug == "" {
		slug = slugify(name)
	}
	return name, slug, true
}
