package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"encoding/json"

	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
)

// serveInternalSessions handles the writer-internal session endpoints the
// CQRS readers call for every session mutation (create / get-or-refresh /
// delete / delete-by-sid / delete-by-sub). Requests are authenticated by an
// HMAC over the exact request body keyed with the shared session secret, plus
// a signed timestamp bounding replay — no cookies, no API keys. Refresh runs
// here, on the single writer, so the single-use iambarn refresh token can
// never be raced across replicas.
func (s *Server) serveInternalSessions(w http.ResponseWriter, r *http.Request) {
	if len(s.internalSessionSecret) == 0 || s.sessionStore == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !sessionstore.VerifyBody(s.internalSessionSecret, body, r.Header.Get(sessionstore.AuthHeader)) {
		s.logger.Warn("internal sessions: bad request signature", "path", r.URL.Path)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req sessionstore.Request
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	if !sessionstore.FreshTS(req.TS, time.Now()) {
		s.logger.Warn("internal sessions: stale request timestamp", "path", r.URL.Path)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	op := strings.TrimPrefix(r.URL.Path, sessionstore.InternalPathPrefix)
	s.dispatchInternalSessionOp(w, r, op, req)
}

// dispatchInternalSessionOp executes one authenticated internal session
// operation and writes the wire response.
func (s *Server) dispatchInternalSessionOp(w http.ResponseWriter, r *http.Request, op string, req sessionstore.Request) {
	ctx := r.Context()
	switch op {
	case "create":
		if req.Session == nil {
			http.Error(w, "missing session", http.StatusBadRequest)
			return
		}
		if err := s.sessionStore.Create(ctx, *req.Session); err != nil {
			s.logger.Error("internal sessions: create failed", "error", err)
			http.Error(w, "create failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, sessionstore.Response{Status: sessionstore.StatusOK})
	case "get-or-refresh":
		s.internalGetOrRefresh(w, r, req.IDHash)
	case "delete":
		if err := s.sessionStore.Delete(ctx, req.IDHash); err != nil {
			s.logger.Error("internal sessions: delete failed", "error", err)
			http.Error(w, "delete failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, sessionstore.Response{Status: sessionstore.StatusOK})
	case "delete-by-sid", "delete-by-sub":
		s.internalDeleteBy(w, r, op, req)
	default:
		http.NotFound(w, r)
	}
}

// internalGetOrRefresh loads a session and refreshes its tokens when needed,
// mapping the store's error taxonomy onto HTTP statuses the Remote client
// understands: 200 ok / 200 transient (stale row, grace applies caller-side),
// 401 revoked, 404 unknown.
func (s *Server) internalGetOrRefresh(w http.ResponseWriter, r *http.Request, idHash string) {
	ws, err := s.sessionStore.Refresh(r.Context(), idHash)
	switch {
	case err == nil:
		writeJSON(w, sessionstore.Response{Status: sessionstore.StatusOK, Session: &ws})
	case errors.Is(err, sessionstore.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, sessionstore.ErrRevoked):
		http.Error(w, "revoked", http.StatusUnauthorized)
	default:
		// Transient IdP failure: hand the stale row back with the outage
		// marker so the reader can apply the bounded-grace policy.
		writeJSON(w, sessionstore.Response{
			Status:  sessionstore.StatusTransient,
			Error:   err.Error(),
			Session: &ws,
		})
	}
}

// internalDeleteBy handles the bulk deletions used by back-channel logout.
func (s *Server) internalDeleteBy(w http.ResponseWriter, r *http.Request, op string, req sessionstore.Request) {
	var n int64
	var err error
	if op == "delete-by-sid" {
		n, err = s.sessionStore.DeleteBySID(r.Context(), req.SID)
	} else {
		n, err = s.sessionStore.DeleteBySub(r.Context(), req.Sub)
	}
	if err != nil {
		s.logger.Error("internal sessions: bulk delete failed", "op", op, "error", err)
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, sessionstore.Response{Status: sessionstore.StatusOK, Deleted: n})
}
