package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/keytouch"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
)

// maxKeyTouches bounds one batch. A reader sends one entry per distinct key it
// served in the interval, so a real batch is a handful.
const maxKeyTouches = 10000

// keyTouchApplyTimeout bounds the writer's apply of one batch.
const keyTouchApplyTimeout = 30 * time.Second

// KeyToucher applies API key last-use times. The storage layer implements it.
type KeyToucher interface {
	TouchAPIKeysAt(ctx context.Context, touches []domain.APIKeyTouch) error
}

// SetKeyToucher enables the writer-internal endpoint the readers use to report
// API key use. Writer/single-process only.
func (s *Server) SetKeyToucher(t KeyToucher) { s.keyToucher = t }

// serveInternalEndpoint routes the writer-internal endpoints that answer only to
// HMAC-signed reader requests. It reports whether the path was one of them.
func (s *Server) serveInternalEndpoint(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case strings.HasPrefix(r.URL.Path, sessionstore.InternalPathPrefix):
		s.serveInternalSessions(w, r)
	case r.URL.Path == keytouch.Path:
		s.serveInternalKeyTouch(w, r)
	default:
		return false
	}
	return true
}

// serveInternalKeyTouch applies one batch of API key last-use times from a
// reader. It shares the signing scheme of the internal session endpoints.
func (s *Server) serveInternalKeyTouch(w http.ResponseWriter, r *http.Request) {
	if len(s.internalSessionSecret) == 0 || s.keyToucher == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !sessionstore.VerifyBody(s.internalSessionSecret, body, r.Header.Get(sessionstore.AuthHeader)) {
		s.logger.Warn("internal api key touch: bad request signature")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var batch keytouch.Batch
	if err := json.Unmarshal(body, &batch); err != nil || len(batch.Touches) > maxKeyTouches {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	if !sessionstore.FreshTS(batch.TS, time.Now()) {
		s.logger.Warn("internal api key touch: stale request timestamp")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// A reader gives up after 10s and sends the batch again on its next tick, so
	// the apply outlives the request under its own deadline. Each touch only
	// moves last_used_at forward, which makes the repeat harmless.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), keyTouchApplyTimeout)
	defer cancel()
	if err := s.keyToucher.TouchAPIKeysAt(ctx, batch.Touches); err != nil {
		if !clientGone(err) {
			s.logger.Error("internal api key touch: apply failed", "error", err)
		}
		http.Error(w, "touch failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
