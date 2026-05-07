package api

import (
	"fmt"
	"net/http"
	"os"
)

// serveDBBackup streams the SQLite database file to the client.
// Used by reader pods as a fallback when Litestream restore fails.
func (s *Server) serveDBBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dbPath := s.dbPath
	if dbPath == "" {
		http.Error(w, "backup not available", http.StatusServiceUnavailable)
		return
	}

	f, err := os.Open(dbPath)
	if err != nil {
		s.logger.Error("db backup: open failed", "error", err)
		http.Error(w, "backup not available", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		s.logger.Error("db backup: stat failed", "error", err)
		http.Error(w, "backup not available", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-sqlite3")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	http.ServeContent(w, r, "bugbarn.db", stat.ModTime(), f)
}
