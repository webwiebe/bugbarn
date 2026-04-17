package api

import (
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// serveFacetsRoute handles GET /api/v1/facets and GET /api/v1/facets/{key}.
func (s *Server) serveFacetsRoute(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	// Trim the base prefix then check whether a key is present.
	suffix := strings.TrimPrefix(r.URL.Path, "/api/v1/facets")
	suffix = strings.TrimPrefix(suffix, "/")

	if suffix == "" {
		// GET /api/v1/facets — list all facet keys.
		projectID, ok := storage.ProjectIDFromContext(r.Context())
		if !ok {
			projectID = s.store.DefaultProjectID()
		}
		keys, err := s.service.ListFacetKeys(r.Context(), projectID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		if keys == nil {
			keys = []string{}
		}
		writeJSON(w, map[string]any{"keys": keys})
		return
	}

	// GET /api/v1/facets/{key} — list values for a key.
	projectID, ok := storage.ProjectIDFromContext(r.Context())
	if !ok {
		projectID = s.store.DefaultProjectID()
	}
	values, err := s.service.ListFacetValues(r.Context(), projectID, suffix)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	if values == nil {
		values = []string{}
	}
	writeJSON(w, map[string]any{"key": suffix, "values": values})
}
