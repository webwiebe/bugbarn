package api

import (
	"fmt"
	"net/http"
	"os"
)

// serveDBBackup streams the SQLite database file to the client.
//
// This dumps the entire database — every project's events/logs (which routinely
// contain PII and secrets) plus the api_keys table — so it is gated well beyond
// ordinary read access: it requires a valid admin session (a scoped API key is
// rejected, even a read-scope one) and, when trusted proxies are configured, the
// request must originate from one. The route is registered under the
// authenticated dispatch, so unauthenticated callers never reach this handler.
func (s *Server) serveDBBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Full DB export is admin-only: require a session, not just any API key.
	// When auth is disabled entirely (s.users == nil / !Enabled) the whole API is
	// open, so we stay consistent with the rest of the surface and allow it.
	if s.users != nil && s.users.Enabled() {
		if _, ok := s.sessionUser(r); !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	// Defense in depth: when trusted proxies are configured, only accept the
	// backup request from within that trusted network (e.g. internal reader pods).
	if len(s.trustedProxies) > 0 && !isTrustedProxy(remoteHost(r.RemoteAddr), s.trustedProxies) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
