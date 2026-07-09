package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/logparse"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Bounds for the log-ingest endpoint. maxLogsBodyBytes falls back to this when
// the ingest handler (which owns the shared body cap) is not wired in.
const (
	defaultMaxLogsBodyBytes = 1 << 20 // 1 MiB
	maxLogEntries           = 10000
)

// maxLogsBodyBytes returns the request-body cap for log ingest, reusing the
// ingest handler's configured cap when available so both ingest paths agree.
func (s *Server) maxLogsBodyBytes() int64 {
	if s.ingestHandler != nil {
		if n := s.ingestHandler.MaxBodyBytes(); n > 0 {
			return n
		}
	}
	return defaultMaxLogsBodyBytes
}

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

	// Bound the request: cap total body size and the number of entries so a
	// single caller cannot force unbounded heap growth on the shared writer.
	body := http.MaxBytesReader(w, r.Body, s.maxLogsBodyBytes())

	rawEntries, ok := s.readLogEntries(w, body, ct)
	if !ok {
		return
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

// readLogEntries parses the (already size-capped) body into raw log-entry maps
// per content type, enforcing the entry-count cap. ok is false when it has
// already written an error response.
func (s *Server) readLogEntries(w http.ResponseWriter, body io.Reader, contentType string) ([]map[string]any, bool) {
	if contentType == "application/x-ndjson" {
		return s.readNDJSONLogs(w, body)
	}
	return s.readJSONLogs(w, body)
}

func (s *Server) readNDJSONLogs(w http.ResponseWriter, body io.Reader) ([]map[string]any, bool) {
	var rawEntries []map[string]any
	scanner := bufio.NewScanner(body)
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
		if len(rawEntries) > maxLogEntries {
			http.Error(w, "too many log entries", http.StatusRequestEntityTooLarge)
			return nil, false
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, s.writeLogReadError(w, err)
	}
	return rawEntries, true
}

func (s *Server) readJSONLogs(w http.ResponseWriter, body io.Reader) ([]map[string]any, bool) {
	var payload struct {
		Logs []map[string]any `json:"logs"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return nil, s.writeLogReadError(w, err)
	}
	if len(payload.Logs) > maxLogEntries {
		http.Error(w, "too many log entries", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return payload.Logs, true
}

// writeLogReadError maps a body read/decode failure to 413 (over the size cap)
// or 400, and always reports false so the caller stops.
func (s *Server) writeLogReadError(w http.ResponseWriter, err error) bool {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		http.Error(w, "log payload too large", http.StatusRequestEntityTooLarge)
		return false
	}
	s.logger.Warn("logs: invalid payload", "error", err)
	http.Error(w, "invalid log payload", http.StatusBadRequest)
	return false
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
