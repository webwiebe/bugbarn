package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// pinoLevelNames maps Pino numeric levels to level name strings.
var pinoLevelNames = map[int]string{
	10: "trace",
	20: "debug",
	30: "info",
	40: "warn",
	50: "error",
	60: "fatal",
}

// pinoLevelNums maps level name strings to Pino numeric levels.
var pinoLevelNums = map[string]int{
	"trace": 10,
	"debug": 20,
	"info":  30,
	"warn":  40,
	"error": 50,
	"fatal": 60,
}

// skipFields are fields stripped from the Data map during ingest.
var skipFields = map[string]bool{
	"v":        true,
	"pid":      true,
	"hostname": true,
	"msg":      true,
	"message":  true,
	"level":    true,
	"time":     true,
}

// parsePinoObject converts a raw JSON object from a Pino-style log payload
// into a LogEntry. projectID must be set by the caller.
func parsePinoObject(obj map[string]any, projectID int64) storage.LogEntry {
	entry := storage.LogEntry{
		ProjectID:  projectID,
		ReceivedAt: time.Now().UTC(),
		LevelNum:   30,
		Level:      "info",
		Data:       make(map[string]any),
	}

	// Extract message from "msg" or "message".
	if msg, ok := obj["msg"].(string); ok {
		entry.Message = msg
	} else if msg, ok := obj["message"].(string); ok {
		entry.Message = msg
	}

	// Extract level — may be a number or string.
	if lvl, ok := obj["level"]; ok {
		switch v := lvl.(type) {
		case float64:
			entry.LevelNum = int(v)
			if name, ok := pinoLevelNames[entry.LevelNum]; ok {
				entry.Level = name
			} else {
				entry.Level = strconv.Itoa(entry.LevelNum)
			}
		case string:
			entry.Level = strings.ToLower(v)
			if num, ok := pinoLevelNums[entry.Level]; ok {
				entry.LevelNum = num
			}
		}
	}

	// Extract timestamp from "time" field (epoch ms).
	if t, ok := obj["time"].(float64); ok {
		ms := int64(t)
		entry.ReceivedAt = time.UnixMilli(ms).UTC()
	}

	// Collect remaining fields into Data, excluding reserved keys.
	for k, v := range obj {
		if !skipFields[k] {
			entry.Data[k] = v
		}
	}
	if len(entry.Data) == 0 {
		entry.Data = nil
	}

	return entry
}

// levelMinFromName converts a level name to the minimum numeric level for filtering.
func levelMinFromName(name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	if num, ok := pinoLevelNums[name]; ok {
		return num
	}
	return 0
}

// serveLogsIngest handles POST /api/v1/logs
func (s *Server) serveLogsIngest(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}

	projectID, ok := storage.ProjectIDFromContext(r.Context())
	if !ok || projectID == 0 {
		http.Error(w, "project required: provide X-BugBarn-Project header or use a project-scoped API key", http.StatusBadRequest)
		return
	}

	ct := r.Header.Get("Content-Type")
	// Strip params like "; charset=utf-8"
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
				continue // skip malformed lines
			}
			rawEntries = append(rawEntries, obj)
		}
	default:
		// Default: application/json with {"logs":[...]}
		var payload struct {
			Logs []map[string]any `json:"logs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
		rawEntries = payload.Logs
	}

	if len(rawEntries) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	entries := make([]storage.LogEntry, 0, len(rawEntries))
	for _, obj := range rawEntries {
		entries = append(entries, parsePinoObject(obj, projectID))
	}

	if err := s.store.InsertLogEntries(r.Context(), entries); err != nil {
		writeStorageError(w, err)
		return
	}

	if s.logHub != nil {
		for _, e := range entries {
			s.logHub.Publish(projectID, e)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// serveLogs handles GET /api/v1/logs
func (s *Server) serveLogs(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}

	projectID, ok := storage.ProjectIDFromContext(r.Context())
	if !ok || projectID == 0 {
		http.Error(w, "project required", http.StatusBadRequest)
		return
	}

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
		levelMin = levelMinFromName(v)
	}

	q := r.URL.Query().Get("q")

	var beforeID int64
	if v := r.URL.Query().Get("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			beforeID = n
		}
	}

	entries, err := s.store.ListLogEntries(r.Context(), projectID, levelMin, q, limit, beforeID)
	if err != nil {
		writeStorageError(w, err)
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

// serveLogsStream handles GET /api/v1/logs/stream (SSE)
func (s *Server) serveLogsStream(w http.ResponseWriter, r *http.Request) {
	if s.logHub == nil {
		http.Error(w, "log streaming unavailable", http.StatusServiceUnavailable)
		return
	}

	projectID, ok := storage.ProjectIDFromContext(r.Context())
	if !ok || projectID == 0 {
		http.Error(w, "project required", http.StatusBadRequest)
		return
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
