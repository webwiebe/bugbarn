package api

import (
	"net/http"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// archiveProject handles POST /api/v1/projects/:slug/archive. It refuses a
// project that received data in the last 7 days and otherwise answers with the
// time of its last event, log line or page view.
func (s *Server) archiveProject(w http.ResponseWriter, r *http.Request) {
	last, err := s.projects.Archive(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resp := map[string]any{"ok": true, "last_event_at": nil}
	if !last.IsZero() {
		resp["last_event_at"] = last.UTC().Format(time.RFC3339)
	}
	writeJSON(w, resp)
}

// unarchiveProject handles POST /api/v1/projects/:slug/unarchive.
func (s *Server) unarchiveProject(w http.ResponseWriter, r *http.Request) {
	if err := s.projects.Unarchive(r.Context(), r.PathValue("slug")); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// withoutArchived drops archived projects unless the caller asked for them with
// include_archived=true. Archived data stays readable by slug; the list is what
// the project picker shows.
func withoutArchived(r *http.Request, projects []domain.Project) []domain.Project {
	if r.URL.Query().Get("include_archived") == "true" {
		return projects
	}
	kept := make([]domain.Project, 0, len(projects))
	for _, p := range projects {
		if p.Status != domain.ProjectStatusArchived {
			kept = append(kept, p)
		}
	}
	return kept
}
