package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// A project-bound API key that is not full-scope is confined to its project on
// the authenticated routes (#235): a request that names another project through
// X-BugBarn-Project or project_slug is answered 403, and an issue of another
// project is answered 404, so a leaked key reaches one project only. Full-scope
// keys, user sessions and unbound keys (including the shared static key) stay
// cross-project.

type boundProjectKey struct{}

// withBoundProject marks ctx as belonging to a caller confined to projectID.
func withBoundProject(ctx context.Context, projectID int64) context.Context {
	return context.WithValue(ctx, boundProjectKey{}, projectID)
}

// boundProject returns the project the caller is confined to, if any.
func boundProject(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(boundProjectKey{}).(int64)
	return id, ok && id > 0
}

// confinedProject returns the project an API key is confined to: its own, when
// the key is bound and not full-scope.
func confinedProject(projectID int64, scope string) (int64, bool) {
	if projectID > 0 && scope != domain.APIKeyScopeFull {
		return projectID, true
	}
	return 0, false
}

// confineBoundKey marks the request as confined when it authenticated with a
// bound, non-full-scope key and no session, and answers 403 when it names
// another project. It returns false after writing the response.
func (s *Server) confineBoundKey(
	w http.ResponseWriter, r *http.Request, usingSession, usingAPIKey bool, keyProjectID int64, scope string,
) (*http.Request, bool) {
	bound, confined := confinedProject(keyProjectID, scope)
	if !confined || !usingAPIKey || usingSession {
		return r, true
	}
	r = r.WithContext(withBoundProject(r.Context(), bound))
	return r, s.confineProjectNames(w, r)
}

var errForeignProject = errors.New("api key is bound to another project")

// projectForSlug resolves a project slug named by the request. A confined caller
// gets errForeignProject for any slug that is not its own project, and never
// creates a project; everyone else gets the usual find-or-create.
func (s *Server) projectForSlug(r *http.Request, slug string) (domain.Project, error) {
	bound, confined := boundProject(r.Context())
	if !confined {
		return s.projects.Ensure(r.Context(), slug)
	}
	proj, err := s.projects.BySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return domain.Project{}, errForeignProject
		}
		return domain.Project{}, err
	}
	if proj.ID != bound {
		return domain.Project{}, errForeignProject
	}
	return proj, nil
}

// requireIssueInScope answers 404 for an issue of another project when the
// caller is confined, so the answer does not reveal that the issue exists. It
// returns false after writing the response.
func (s *Server) requireIssueInScope(w http.ResponseWriter, r *http.Request, issueID string) bool {
	bound, confined := boundProject(r.Context())
	if !confined {
		return true
	}
	issue, err := s.issues.Get(r.Context(), issueID)
	if err != nil {
		writeServiceError(w, err)
		return false
	}
	if issue.ProjectID != bound {
		http.Error(w, "not found", http.StatusNotFound)
		return false
	}
	return true
}

// issueIDFromPath extracts the display ID from /api/v1/issues/{id}[/suffix].
func issueIDFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/api/v1/issues/")
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
