package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/logparse"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func (s *Server) serveLogsIngest(w http.ResponseWriter, r *http.Request) {
	projectID, ok := storage.ProjectIDFromContext(r.Context())
	if !ok || projectID == 0 {
		http.Error(w, "project required: provide X-BugBarn-Project header or use a project-scoped API key", http.StatusBadRequest)
		return
	}

	ct := r.Header.Get("Content-Type")
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}

	var rawEntries []map[string]any

	switch ct {
	case "application/x-ndjson":
		scanner := bufio.NewScanner(r.Body)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				continue
			}
			rawEntries = append(rawEntries, obj)
		}
	default:
		var payload struct {
			Logs []map[string]any `json:"logs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			s.logger.Warn("logs: invalid JSON payload", "error", err)
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
		rawEntries = payload.Logs
	}

	if len(rawEntries) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	entries := make([]domain.LogEntry, 0, len(rawEntries))
	for _, obj := range rawEntries {
		entries = append(entries, logparse.ParseObject(obj, projectID))
	}

	if err := s.logs.Insert(r.Context(), entries); err != nil {
		writeServiceError(w, err)
		return
	}

	if s.logHub != nil {
		for _, e := range entries {
			s.logHub.Publish(projectID, e)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveLogs(w http.ResponseWriter, r *http.Request) {
	projectID, _ := storage.ProjectIDFromContext(r.Context())

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limit = n
		}
	}

	levelMin := 0
	if v := r.URL.Query().Get("level"); v != "" {
		levelMin = logparse.LevelMinFromName(v)
	}

	q := r.URL.Query().Get("q")

	var beforeID int64
	if v := r.URL.Query().Get("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			beforeID = n
		}
	}

	entries, err := s.logs.List(r.Context(), projectID, levelMin, q, limit, beforeID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	var nextCursor int64
	if len(entries) > 0 {
		nextCursor = entries[len(entries)-1].ID
	}

	writeJSON(w, map[string]any{
		"logs":        entries,
		"next_cursor": nextCursor,
	})
}

func (s *Server) serveLogsStream(w http.ResponseWriter, r *http.Request) {
	if s.logHub == nil {
		http.Error(w, "log streaming unavailable", http.StatusServiceUnavailable)
		return
	}

	projectID, _ := storage.ProjectIDFromContext(r.Context())

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

	ch, cancel := s.logHub.Subscribe(projectID)
	defer cancel()

	ctx := r.Context()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ":\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
