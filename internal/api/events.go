package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) listIssueEvents(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/events")

	limit := 25
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	var beforeID int64
	if v := r.URL.Query().Get("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			beforeID = n
		}
	}

	events, hasMore, err := s.issues.ListEvents(r.Context(), issueID, limit, beforeID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"events": events, "hasMore": hasMore})
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimPrefix(r.URL.Path, "/api/v1/events/")
	event, err := s.issues.GetEvent(r.Context(), eventID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"event": event})
}

func (s *Server) listRecentEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	since := time.Now().UTC().Add(-15 * time.Minute)
	if raw := r.URL.Query().Get("since"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			since = parsed
		}
	}
	if raw := r.URL.Query().Get("window"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			since = time.Now().UTC().Add(-parsed)
		}
	}
	events, err := s.issues.ListLiveEvents(r.Context(), limit, since)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"events": events})
}

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	since := time.Now().UTC().Add(-15 * time.Minute)
	if raw := r.URL.Query().Get("since"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			since = parsed.UTC()
		} else if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			since = parsed.UTC()
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	pollTicker := time.NewTicker(3 * time.Second)
	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer pollTicker.Stop()
	defer keepaliveTicker.Stop()

	cursor := since

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepaliveTicker.C:
			if _, err := fmt.Fprint(w, ":\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-pollTicker.C:
			events, err := s.issues.ListLiveEvents(ctx, 100, cursor)
			if err != nil {
				return
			}
			for i := len(events) - 1; i >= 0; i-- {
				ev := events[i]
				ts := ev.ReceivedAt
				if ev.ObservedAt.After(ts) {
					ts = ev.ObservedAt
				}
				if !ts.After(cursor) {
					continue
				}
				data, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
					return
				}
				if ts.After(cursor) {
					cursor = ts
				}
			}
			flusher.Flush()
		}
	}
}
