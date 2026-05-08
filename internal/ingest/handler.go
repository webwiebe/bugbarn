package ingest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

type Handler struct {
	auth         *auth.Authorizer
	spool        *spool.Spool
	maxBodyBytes int64
	now          func() time.Time
	idFn         func() string
}

func NewHandler(authorizer *auth.Authorizer, eventSpool *spool.Spool, maxBodyBytes int64) *Handler {
	if maxBodyBytes <= 0 {
		maxBodyBytes = 1 << 20
	}
	return &Handler{
		auth:         authorizer,
		spool:        eventSpool,
		maxBodyBytes: maxBodyBytes,
		now:          time.Now,
		idFn:         generateIngestID,
	}
}

// Start blocks until ctx is cancelled. It exists for backwards compatibility
// with callers that run it as a goroutine; the actual spool write now happens
// synchronously inside ServeHTTP.
func (h *Handler) Start(ctx context.Context) {
	<-ctx.Done()
}

func (h *Handler) ValidAPIKey(r *http.Request) bool {
	_, ok := h.APIKeyProject(r)
	return ok
}

// APIKeyProject validates the API key from the request and returns the
// associated project ID. For env-var static keys, projectID=0 is returned.
// Both full-scope and ingest-scope keys are accepted here.
func (h *Handler) APIKeyProject(r *http.Request) (projectID int64, ok bool) {
	pid, _, valid := h.APIKeyProjectScope(r)
	return pid, valid
}

// APIKeyProjectScope validates the API key and returns project ID, scope, and ok.
func (h *Handler) APIKeyProjectScope(r *http.Request) (projectID int64, scope string, ok bool) {
	if h == nil || h.auth == nil {
		return 0, "full", true
	}
	slug := r.Header.Get("x-bugbarn-project")
	return h.auth.ValidWithSetupFallback(r.Context(), r.Header.Get(auth.HeaderAPIKey), slug)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracing.Tracer().Start(r.Context(), "ingest.Receive")
	defer span.End()
	r = r.WithContext(ctx)

	if h == nil || h.auth == nil || h.spool == nil {
		http.Error(w, "ingest unavailable", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodPost:
	default:
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.ValidAPIKey(r) {
		span.SetStatus(codes.Error, "unauthorized")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	defer r.Body.Close()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBodyBytes))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}

		http.Error(w, "unable to read request body", http.StatusBadRequest)
		return
	}

	record := spool.Record{
		IngestID:      h.idFn(),
		ReceivedAt:    h.now().UTC(),
		ContentType:   r.Header.Get("Content-Type"),
		RemoteAddr:    r.RemoteAddr,
		ContentLength: int64(len(body)),
		BodyBase64:    base64.StdEncoding.EncodeToString(body),
		ProjectSlug:   r.Header.Get("x-bugbarn-project"),
	}

	span.SetAttributes(
		attribute.String("ingest_id", record.IngestID),
		attribute.String("project_slug", record.ProjectSlug),
		attribute.Int64("content_length", record.ContentLength),
	)

	_, spoolSpan := tracing.Tracer().Start(ctx, "ingest.SpoolAppend")
	if err := h.spool.Append(record); err != nil {
		spoolSpan.SetStatus(codes.Error, err.Error())
		spoolSpan.End()
		if errors.Is(err, spool.ErrFull) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "ingest spool full", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "ingest unavailable", http.StatusServiceUnavailable)
		return
	}
	spoolSpan.End()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accepted": true,
		"ingestId": record.IngestID,
	})
}

func generateIngestID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000Z") + "-fallback"
	}

	return hex.EncodeToString(raw[:])
}
