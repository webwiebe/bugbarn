package api

import (
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// serveSurfaces answers GET /api/v1/surfaces: the issues seen per value of the
// `surface` attribute in the last 24 hours and 7 days.
func (s *Server) serveSurfaces(w http.ResponseWriter, r *http.Request) {
	projectID, ok := domain.ProjectIDFromContext(r.Context())
	if !ok {
		projectID = s.projects.DefaultProjectID()
	}
	rows, err := s.issues.SurfaceActivity(r.Context(), projectID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if rows == nil {
		rows = []domain.SurfaceActivity{}
	}
	writeJSON(w, map[string]any{"surfaces": rows})
}

// listFacetKeys answers GET /api/v1/facets: the attribute keys seen in the project.
func (s *Server) listFacetKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.issues.ListFacetKeys(r.Context(), s.scopedProjectID(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if keys == nil {
		keys = []string{}
	}
	writeJSON(w, map[string]any{"keys": keys})
}

// listFacetValues answers GET /api/v1/facets/{key}: the values seen for one key.
func (s *Server) listFacetValues(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	values, err := s.issues.ListFacetValues(r.Context(), s.scopedProjectID(r), key)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if values == nil {
		values = []string{}
	}
	writeJSON(w, map[string]any{"key": key, "values": values})
}

// scopedProjectID is the request's resolved project, or the default project.
func (s *Server) scopedProjectID(r *http.Request) int64 {
	if id, ok := domain.ProjectIDFromContext(r.Context()); ok {
		return id
	}
	return s.projects.DefaultProjectID()
}
