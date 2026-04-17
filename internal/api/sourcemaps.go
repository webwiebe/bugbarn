package api

import (
	"io"
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func (s *Server) listSourceMaps(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := s.service.ListSourceMaps(r.Context())
	if err != nil {
		writeStorageError(w, err)
		return
	}
	if items == nil {
		items = []storage.SourceMapMeta{}
	}
	writeJSON(w, map[string]any{"sourceMaps": items})
}

func (s *Server) uploadSourceMap(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid source map payload", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("source_map")
	if err != nil {
		http.Error(w, "source_map is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	blob, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "unable to read source map", http.StatusBadRequest)
		return
	}

	upload := storage.SourceMapUpload{
		Release:     r.FormValue("release"),
		Dist:        r.FormValue("dist"),
		BundleURL:   r.FormValue("bundle_url"),
		Name:        r.FormValue("source_map_name"),
		ContentType: header.Header.Get("Content-Type"),
		Blob:        blob,
	}
	item, err := s.service.UploadSourceMap(r.Context(), upload)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]any{
		"accepted":   true,
		"artifactId": item.ID,
		"release":    item.Release,
		"dist":       item.Dist,
		"bundleUrl":  item.BundleURL,
	})
}
