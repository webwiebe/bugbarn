package api

import (
	"net/http"
	"strings"

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

func (s *Server) serveFacetsRoute(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/v1/facets")
	suffix = strings.TrimPrefix(suffix, "/")

	if suffix == "" {
		projectID, ok := domain.ProjectIDFromContext(r.Context())
		if !ok {
			projectID = s.projects.DefaultProjectID()
		}
		keys, err := s.issues.ListFacetKeys(r.Context(), projectID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if keys == nil {
			keys = []string{}
		}
		writeJSON(w, map[string]any{"keys": keys})
		return
	}

	projectID, ok := domain.ProjectIDFromContext(r.Context())
	if !ok {
		projectID = s.projects.DefaultProjectID()
	}
	values, err := s.issues.ListFacetValues(r.Context(), projectID, suffix)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if values == nil {
		values = []string{}
	}
	writeJSON(w, map[string]any{"key": suffix, "values": values})
}
