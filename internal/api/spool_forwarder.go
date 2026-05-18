package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// spooledRequest is a forwardable HTTP write captured by the reader.
type spooledRequest struct {
	ReceivedAt time.Time         `json:"receivedAt"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers,omitempty"`
	BodyBase64 string            `json:"bodyBase64"`
}

// SpoolForwarder buffers fire-and-forget write requests to disk and drains them
// to the upstream writer in the background. Used by reader pods so that ingest
// keeps accepting traffic while the writer is restarting (deploys etc.).
//
// On-disk format is line-delimited JSON; a cursor file tracks the byte offset
// of the last successfully forwarded record. The active segment is rotated
// when it exceeds rotateBytes so we never replay an unbounded log.
type SpoolForwarder struct {
	dir         string
	writerURL   string
	maxBodyByte int64
	rotateBytes int64
	logger      *slog.Logger
	client      *http.Client

	mu      sync.Mutex
	file    *os.File
	path    string
	pending atomic.Int64 // records appended but not yet acked
}

const (
	spoolFileName       = "forward.ndjson"
	spoolCursorFileName = "forward-cursor.json"
	defaultRotateBytes  = 64 * 1024 * 1024
)

// forwardedHeaders is the allowlist of headers we replay to the writer.
// Restricted to the set the ingest/logs/analytics endpoints actually look at.
var forwardedHeaders = []string{
	"Content-Type",
	"X-Bugbarn-Api-Key",
	"X-Bugbarn-Project",
	"User-Agent",
}

func NewSpoolForwarder(dir, writerURL string, maxBodyBytes int64, logger *slog.Logger) (*SpoolForwarder, error) {
	if dir == "" {
		return nil, errors.New("spool dir is required")
	}
	if writerURL == "" {
		return nil, errors.New("writer url is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}
	path := filepath.Join(dir, spoolFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open spool file: %w", err)
	}
	return &SpoolForwarder{
		dir:         dir,
		writerURL:   writerURL,
		maxBodyByte: maxBodyBytes,
		rotateBytes: defaultRotateBytes,
		logger:      logger,
		client:      &http.Client{Timeout: 30 * time.Second},
		file:        file,
		path:        path,
	}, nil
}

// Forward captures the request body and headers, appends to the spool, and
// responds 202 Accepted. Returns 503 if the spool write fails — the SDK will
// retry, which is the correct behavior for fire-and-forget telemetry.
func (s *SpoolForwarder) Forward(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		var err error
		if s.maxBodyByte > 0 {
			body, err = io.ReadAll(io.LimitReader(r.Body, s.maxBodyByte+1))
			if err == nil && int64(len(body)) > s.maxBodyByte {
				http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
				return
			}
		} else {
			body, err = io.ReadAll(r.Body)
		}
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
	}

	headers := make(map[string]string, len(forwardedHeaders))
	for _, h := range forwardedHeaders {
		if v := r.Header.Get(h); v != "" {
			headers[h] = v
		}
	}

	rec := spooledRequest{
		ReceivedAt: time.Now().UTC(),
		Method:     r.Method,
		Path:       r.URL.RequestURI(),
		Headers:    headers,
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	}

	if err := s.append(rec); err != nil {
		s.logger.Error("spool forward append failed", "error", err)
		http.Error(w, "spool unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"accepted":true}`))
}

func (s *SpoolForwarder) append(rec spooledRequest) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.file.Write(append(payload, '\n')); err != nil {
		return err
	}
	if err := s.file.Sync(); err != nil {
		return err
	}
	s.pending.Add(1)
	return nil
}

// Pending reports the number of records appended but not yet acked by the
// drain loop. Used by shutdown to decide when it is safe to exit.
func (s *SpoolForwarder) Pending() int64 {
	return s.pending.Load()
}

// Drain repeatedly reads new records from the spool and forwards them to the
// writer until the context is cancelled. Records are acked by advancing a
// persisted cursor so a restart resumes where we left off.
func (s *SpoolForwarder) Drain(ctx context.Context) {
	const idleSleep = 250 * time.Millisecond
	const errBackoff = 2 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		offset, err := readCursor(s.dir)
		if err != nil {
			s.logger.Error("spool drain read cursor", "error", err)
			sleepCtx(ctx, errBackoff)
			continue
		}

		records, newOffset, err := readRecords(s.path, offset)
		if err != nil {
			s.logger.Error("spool drain read records", "error", err)
			sleepCtx(ctx, errBackoff)
			continue
		}

		if len(records) == 0 {
			// Spool is empty up to its current end. Rotate when oversized so we
			// don't keep replaying the same file indefinitely.
			if err := s.maybeRotateLocked(newOffset); err != nil {
				s.logger.Error("spool rotate", "error", err)
			}
			sleepCtx(ctx, idleSleep)
			continue
		}

		ackedTo := offset
		for _, r := range records {
			if err := s.forwardOne(ctx, r.req); err != nil {
				if ctx.Err() != nil {
					return
				}
				s.logger.Warn("spool drain forward failed", "error", err, "path", r.req.Path)
				sleepCtx(ctx, errBackoff)
				break
			}
			ackedTo = r.endOffset
			s.pending.Add(-1)
		}

		if ackedTo > offset {
			if err := writeCursor(s.dir, ackedTo); err != nil {
				s.logger.Error("spool drain write cursor", "error", err)
			}
		}
	}
}

// DrainOnce drains everything currently in the spool and returns. Used at
// shutdown so the pod exits with an empty spool. Blocks until the spool is
// empty, the context expires, or a forward fails.
func (s *SpoolForwarder) DrainOnce(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		offset, err := readCursor(s.dir)
		if err != nil {
			return err
		}
		records, _, err := readRecords(s.path, offset)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		ackedTo := offset
		for _, r := range records {
			if err := s.forwardOne(ctx, r.req); err != nil {
				return err
			}
			ackedTo = r.endOffset
			s.pending.Add(-1)
		}
		if err := writeCursor(s.dir, ackedTo); err != nil {
			return err
		}
	}
}

func (s *SpoolForwarder) forwardOne(ctx context.Context, rec spooledRequest) error {
	body, err := base64.StdEncoding.DecodeString(rec.BodyBase64)
	if err != nil {
		// Record is corrupt — drop it so we don't get stuck.
		s.logger.Error("spool record corrupt, dropping", "error", err, "path", rec.Path)
		return nil
	}
	url := s.writerURL + rec.Path
	req, err := http.NewRequestWithContext(ctx, rec.Method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range rec.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("writer returned %d", resp.StatusCode)
	}
	// 4xx is a permanent failure for this record — drop and advance.
	if resp.StatusCode >= 400 {
		s.logger.Warn("writer rejected spooled request", "status", resp.StatusCode, "path", rec.Path)
	}
	return nil
}

func (s *SpoolForwarder) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *SpoolForwarder) maybeRotateLocked(currentEnd int64) error {
	if s.rotateBytes <= 0 || currentEnd < s.rotateBytes {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.file.Close(); err != nil {
		return err
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := resetCursor(s.dir); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	s.file = file
	return nil
}

type spooledRequestAt struct {
	req       spooledRequest
	endOffset int64
}

func readRecords(path string, offset int64) ([]spooledRequestAt, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, offset, nil
		}
		return nil, offset, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, offset, err
	}
	if offset > info.Size() {
		offset = 0
	}
	if offset > 0 {
		if _, err := file.Seek(offset, 0); err != nil {
			return nil, offset, err
		}
	}
	var out []spooledRequestAt
	pos := offset
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		pos += int64(len(line)) + 1
		if len(line) == 0 {
			continue
		}
		var rec spooledRequest
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, pos, err
		}
		out = append(out, spooledRequestAt{req: rec, endOffset: pos})
	}
	if err := scanner.Err(); err != nil {
		return nil, pos, err
	}
	return out, pos, nil
}

type cursorState struct {
	Offset int64 `json:"offset"`
}

func readCursor(dir string) (int64, error) {
	data, err := os.ReadFile(filepath.Join(dir, spoolCursorFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var c cursorState
	if err := json.Unmarshal(data, &c); err != nil {
		return 0, err
	}
	return c.Offset, nil
}

func writeCursor(dir string, offset int64) error {
	data, err := json.Marshal(cursorState{Offset: offset})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, spoolCursorFileName), data, 0o600)
}

func resetCursor(dir string) error {
	err := os.Remove(filepath.Join(dir, spoolCursorFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
