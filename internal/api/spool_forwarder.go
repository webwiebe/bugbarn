package api

import (
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/ingestresp"
	"github.com/wiebe-xyz/bugbarn/internal/normalize"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// produceCounter counts reader-side Redis write-queue publish attempts, built
// once from the global meter (see tracing.Meter) rather than recreated per
// call. This is the reader-side mirror of ingestproc's writer-side
// bugbarn.consumer.items counter.
var produceCounter metric.Int64Counter

func init() {
	produceCounter, _ = tracing.Meter().Int64Counter(
		"bugbarn.queue.produce",
		metric.WithDescription("Reader-side write-queue publish attempts, by outcome."),
		metric.WithUnit("{call}"),
	)
}

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
	queue       *queue.RedisQueue // spec 007: when set, drain publishes to Redis instead of HTTP
	maxBodyByte int64
	rotateBytes int64
	logger      *slog.Logger
	client      *http.Client

	mu      sync.Mutex
	file    *os.File
	path    string
	pending atomic.Int64 // records appended but not yet acked

	// resolveSlug maps an ingest API key to the slug of the project it is
	// scoped to, or "" when the key carries no project binding (static
	// env-var keys) or cannot be resolved. Optional; nil disables key-based
	// project resolution.
	resolveSlug func(ctx context.Context, apiKey string) string
}

// SetProjectResolver wires the API-key → project-slug lookup used to stamp a
// project onto spooled requests that carry no X-BugBarn-Project header.
func (s *SpoolForwarder) SetProjectResolver(fn func(ctx context.Context, apiKey string) string) {
	s.resolveSlug = fn
}

const (
	spoolFileName       = "forward.ndjson"
	spoolCursorFileName = "forward-cursor.json"
	defaultRotateBytes  = 64 * 1024 * 1024
)

// Canonical (http.Header.Get) forms of the ingest headers we key the spooled
// record's header map by.
const (
	apiKeyHeader  = "X-Bugbarn-Api-Key"
	projectHeader = "X-Bugbarn-Project"
)

// forwardedHeaders is the allowlist of headers we replay to the writer.
// Restricted to the set the ingest/logs/analytics endpoints actually look at.
var forwardedHeaders = []string{
	"Content-Type",
	apiKeyHeader,
	projectHeader,
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
		client:      &http.Client{Timeout: 5 * time.Second},
		file:        file,
		path:        path,
	}, nil
}

// NewRedisSpoolForwarder is like NewSpoolForwarder but drains the spool to a
// Redis write queue instead of forwarding to the writer over HTTP (spec 007).
// The on-disk spool remains the durability anchor: the cursor advances only
// after a successful publish, so a Redis outage backs the spool up without loss.
func NewRedisSpoolForwarder(dir string, q *queue.RedisQueue, maxBodyBytes int64, logger *slog.Logger) (*SpoolForwarder, error) {
	if dir == "" {
		return nil, errors.New("spool dir is required")
	}
	if q == nil {
		return nil, errors.New("queue is required")
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
		queue:       q,
		maxBodyByte: maxBodyBytes,
		rotateBytes: defaultRotateBytes,
		logger:      logger,
		file:        file,
		path:        path,
	}, nil
}

// Forward captures the request body and headers, appends to the spool, and
// responds 202 Accepted. Returns 503 if the spool write fails — the SDK will
// retry, which is the correct behavior for fire-and-forget telemetry.
//
// Event payloads are validated synchronously before spooling so a malformed
// event is dropped here with a 400 rather than queued and silently discarded by
// the writer. This keeps the accepted/dropped distinction honest even on reader
// pods, which return 202 before the writer ever sees the event.
func (s *SpoolForwarder) Forward(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		var err error
		if s.maxBodyByte > 0 {
			body, err = io.ReadAll(io.LimitReader(r.Body, s.maxBodyByte+1))
			if err == nil && int64(len(body)) > s.maxBodyByte {
				ingestresp.WriteDropped(w, ingestresp.DropTooLarge)
				return
			}
		} else {
			body, err = io.ReadAll(r.Body)
		}
		if err != nil {
			ingestresp.WriteDropped(w, ingestresp.DropMalformed)
			return
		}
	}

	if kindForPath(r.URL.Path) == queue.KindEvent {
		if err := normalize.Validate(body); err != nil {
			ingestresp.WriteDropped(w, ingestresp.DropMalformed)
			return
		}
	}

	headers := make(map[string]string, len(forwardedHeaders))
	for _, h := range forwardedHeaders {
		if v := r.Header.Get(h); v != "" {
			headers[h] = v
		}
	}

	// A project-scoped API key identifies its project on its own, so resolve it
	// here and stamp the slug onto the record: only the header survives into
	// queue.Item, and the consumer drops any item with an empty slug. The
	// header, when present, wins — it is the caller's explicit override.
	if headers[projectHeader] == "" && s.resolveSlug != nil {
		if slug := s.resolveSlug(r.Context(), headers[apiKeyHeader]); slug != "" {
			headers[projectHeader] = slug
		}
	}

	// Logs need a project to land in. Reject now rather than accept with a 202
	// and drop the batch in the consumer, which loses the data silently. This
	// mirrors the direct (non-queue) path, which 400s on the same condition.
	//
	// Queue mode only: HTTP mode replays the whole request, API key included, so
	// the writer resolves the project itself and nothing is lost. Only queue.Item
	// narrows the request down to a slug. Events pass either way — they fall back
	// to the Default Project instead of dropping.
	if s.queue != nil && kindForPath(r.URL.Path) == queue.KindLog && headers[projectHeader] == "" {
		http.Error(w, "project required: provide X-BugBarn-Project header or use a project-scoped API key", http.StatusBadRequest)
		return
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
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
		return
	}

	ingestresp.WriteAccepted(w, "")
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
	if s.queue != nil {
		return s.publishOne(ctx, rec)
	}
	ctx, span := tracing.Tracer().Start(ctx, "forwarder.ForwardOne",
		trace.WithAttributes(
			attribute.String("http.target", s.writerURL+rec.Path),
			attribute.String("http.method", rec.Method),
		),
	)
	defer span.End()

	body, err := base64.StdEncoding.DecodeString(rec.BodyBase64)
	if err != nil {
		// Record is corrupt — drop it so we don't get stuck.
		s.logger.Error("spool record corrupt, dropping", "error", err, "path", rec.Path)
		return nil
	}
	url := s.writerURL + rec.Path
	req, err := http.NewRequestWithContext(ctx, rec.Method, url, bytes.NewReader(body))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	for k, v := range rec.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= 500 {
		err := fmt.Errorf("writer returned %d", resp.StatusCode)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	// 4xx is a permanent failure for this record — drop and advance.
	if resp.StatusCode >= 400 {
		s.logger.Warn("writer rejected spooled request", "status", resp.StatusCode, "path", rec.Path)
	}
	return nil
}

// publishOne converts a spooled ingest request into a queue.Item and LPUSHes it.
// Returning nil acks the record (cursor advances); returning an error retries.
func (s *SpoolForwarder) publishOne(ctx context.Context, rec spooledRequest) error {
	kind := kindForPath(rec.Path)
	if kind == "" {
		// Not an ingest path we route through the queue — drop and advance.
		s.logger.Warn("spool record has no queue kind, dropping", "path", rec.Path)
		return nil
	}
	item := queue.Item{
		Kind:        kind,
		ReceivedAt:  rec.ReceivedAt,
		ContentType: rec.Headers["Content-Type"],
		ProjectSlug: rec.Headers[projectHeader],
		BodyBase64:  rec.BodyBase64,
	}
	err := s.queue.Publish(ctx, []queue.Item{item})
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	produceCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	return err
}

// kindForPath maps an ingest request path to its queue Item kind.
func kindForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v1/events"):
		return queue.KindEvent
	case strings.HasPrefix(path, "/api/v1/logs"):
		return queue.KindLog
	default:
		return ""
	}
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
