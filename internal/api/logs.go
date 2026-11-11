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

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/logparse"
	logsvc "github.com/wiebe-xyz/bugbarn/internal/service/logs"
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
	projectID, ok := domain.ProjectIDFromContext(r.Context())
	if !ok || projectID == 0 {
		http.Error(w, "project required: provide X-BugBarn-Project header or use a project-scoped API key", http.StatusBadRequest)
		return
	}

	// Bound the request: cap total body size and the number of entries so a
	// single caller cannot force unbounded heap growth on the shared writer.
	body := http.MaxBytesReader(w, r.Body, s.maxLogsBodyBytes())

	rawEntries, ok := s.readLogEntries(w, body, r.Header.Get("Content-Type"))
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
	if logparse.IsNDJSON(contentType) {
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
		Logs json.RawMessage `json:"logs"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return nil, s.writeLogReadError(w, err)
	}
	entries, err := logparse.LogsArray(payload.Logs)
	if err != nil {
		return nil, s.writeLogReadError(w, err)
	}
	if len(entries) > maxLogEntries {
		http.Error(w, "too many log entries", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return entries, true
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
	msg := "invalid log payload"
	if errors.Is(err, logparse.ErrNoLogsArray) {
		msg += ": " + err.Error()
	}
	http.Error(w, msg, http.StatusBadRequest)
	return false
}

// logQueryFromRequest reads the explorer filters from the query string: level,
// q, from, to (RFC 3339), before, and attr=key=value (repeatable, ANDed). The
// project comes from the request context, as for every other read.
func logQueryFromRequest(r *http.Request) (logsvc.Query, error) {
	projectID, _ := domain.ProjectIDFromContext(r.Context())
	query := r.URL.Query()
	q := logsvc.Query{ProjectID: projectID, Q: query.Get("q")}

	if v := query.Get("level"); v != "" {
		q.LevelMin = logparse.LevelMinFromName(v)
	}
	if v := query.Get("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.BeforeID = n
		}
	}
	for name, dst := range map[string]*time.Time{"from": &q.From, "to": &q.To} {
		if v := query.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return q, apperr.InvalidInput(name+" must be an RFC 3339 time", err)
			}
			*dst = t
		}
	}
	for _, raw := range query["attr"] {
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			return q, apperr.InvalidInput("attr must be key=value", nil)
		}
		q.Attrs = append(q.Attrs, logsvc.AttrFilter{Key: key, Value: value})
	}
	return q, nil
}

func (s *Server) serveLogs(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limit = n
		}
	}

	q, err := logQueryFromRequest(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	entries, err := s.logs.Query(r.Context(), q, limit)
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

// logBucketSteps are the bucket widths the histogram picks from, so a chart
// stays at or under logMaxBuckets bars with round edges.
var logBucketSteps = []time.Duration{
	10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute,
	30 * time.Minute, time.Hour, 2 * time.Hour, 6 * time.Hour, 24 * time.Hour,
}

const (
	logTargetBuckets = 60
	logMaxBuckets    = 200
	logDefaultRange  = 24 * time.Hour
)

// pickLogBucket returns the smallest step that keeps the range under the
// target bar count, or the requested width when it stays under the hard cap.
func pickLogBucket(span time.Duration, requested time.Duration) (time.Duration, error) {
	if requested > 0 {
		if requested < time.Second || span/requested > logMaxBuckets {
			return 0, apperr.InvalidInput("bucket must give at most 200 buckets over the range", nil)
		}
		return requested, nil
	}
	for _, step := range logBucketSteps {
		if span/step <= logTargetBuckets {
			return step, nil
		}
	}
	return logBucketSteps[len(logBucketSteps)-1], nil
}

// serveLogsHistogram answers GET /api/v1/logs/histogram: entry counts per time
// bucket and level over from..to (default: the last 24 hours) for the filters.
func (s *Server) serveLogsHistogram(w http.ResponseWriter, r *http.Request) {
	q, err := logQueryFromRequest(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if q.To.IsZero() {
		q.To = time.Now().UTC().Truncate(time.Second).Add(time.Second)
	}
	if q.From.IsZero() {
		q.From = q.To.Add(-logDefaultRange)
	}
	var requested time.Duration
	if v := r.URL.Query().Get("bucket_seconds"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 {
			writeServiceError(w, apperr.InvalidInput("bucket_seconds must be a positive integer", perr))
			return
		}
		requested = time.Duration(n) * time.Second
	}
	if !q.To.After(q.From) {
		writeServiceError(w, apperr.InvalidInput("from must be before to", nil))
		return
	}
	bucket, err := pickLogBucket(q.To.Sub(q.From), requested)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	h, err := s.logs.Histogram(r.Context(), q, bucket)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h)
}

// serveLogsFacets answers GET /api/v1/logs/facets: the attribute keys of the
// matching entries, or with key=<name> the top 10 values of that key.
func (s *Server) serveLogsFacets(w http.ResponseWriter, r *http.Request) {
	q, err := logQueryFromRequest(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if q.To.IsZero() {
		q.To = time.Now().UTC().Truncate(time.Second).Add(time.Second)
	}
	if q.From.IsZero() {
		q.From = q.To.Add(-logDefaultRange)
	}
	f, err := s.logs.Facets(r.Context(), q, r.URL.Query().Get("key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, f)
}

func (s *Server) serveLogsStream(w http.ResponseWriter, r *http.Request) {
	if s.logHub == nil {
		http.Error(w, "log streaming unavailable", http.StatusServiceUnavailable)
		return
	}

	projectID, _ := domain.ProjectIDFromContext(r.Context())

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
				s.logger.Warn("logs: failed to marshal live log entry; skipping", "log_id", entry.ID, "project_id", entry.ProjectID, "error", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
