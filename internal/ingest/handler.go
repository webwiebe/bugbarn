package ingest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingestresp"
	"github.com/wiebe-xyz/bugbarn/internal/normalize"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// ingestReceivedCounter tags every ingest request by its terminal outcome
// (accepted or one of the ingestresp.Drop reasons). Built once from the
// package-global meter; before tracing.Init wires a MeterProvider it is a
// valid no-op instrument, so construction never fails in tests or when
// telemetry is disabled.
var ingestReceivedCounter, _ = tracing.Meter().Int64Counter(
	"bugbarn.ingest.received",
	metric.WithDescription("Ingest requests received, by outcome."),
	metric.WithUnit("{request}"),
)

// recordIngestReceived reports one ingest request's terminal outcome. outcome
// is either "accepted" or an ingestresp.Drop.Reason value.
func recordIngestReceived(ctx context.Context, outcome string) {
	ingestReceivedCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

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

// RecordKindRelease marks a spool record as a release marker rather than an
// ingest event. The worker dispatches on spool.Record.Kind.
const RecordKindRelease = "release"

// MaxBodyBytes returns the configured maximum request body size.
func (h *Handler) MaxBodyBytes() int64 {
	return h.maxBodyBytes
}

// SpoolRelease enqueues a release-marker payload onto the ingest spool for
// asynchronous creation by the background worker. The raw JSON body is stored
// verbatim and decoded by the worker; projectID is the already-resolved project
// captured at enqueue time. Returns the generated ingest ID.
//
// This keeps release creation off the request path: the worker owns the single
// SQLite writer connection, so a synchronous create contends with event
// persistence and can block for many seconds under load.
func (h *Handler) SpoolRelease(projectID int64, contentType, remoteAddr string, body []byte) (string, error) {
	if h == nil || h.spool == nil {
		return "", errors.New("ingest spool unavailable")
	}
	record := spool.Record{
		IngestID:      h.idFn(),
		ReceivedAt:    h.now().UTC(),
		Kind:          RecordKindRelease,
		ContentType:   contentType,
		RemoteAddr:    remoteAddr,
		ContentLength: int64(len(body)),
		BodyBase64:    base64.StdEncoding.EncodeToString(body),
		ProjectID:     projectID,
	}
	if err := h.spool.Append(record); err != nil {
		return "", err
	}
	return record.IngestID, nil
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
		recordIngestReceived(ctx, ingestresp.DropUnavailable.Reason)
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
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
		recordIngestReceived(ctx, ingestresp.DropUnauthorized.Reason)
		ingestresp.WriteDropped(w, ingestresp.DropUnauthorized)
		return
	}

	defer r.Body.Close()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBodyBytes))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			recordIngestReceived(ctx, ingestresp.DropTooLarge.Reason)
			ingestresp.WriteDropped(w, ingestresp.DropTooLarge)
			return
		}

		recordIngestReceived(ctx, ingestresp.DropMalformed.Reason)
		ingestresp.WriteDropped(w, ingestresp.DropMalformed)
		return
	}

	// Validate synchronously so a malformed event is dropped here with a clear
	// 400 instead of being durably queued and then silently discarded by the
	// worker. A body that passes this check will not fail to parse downstream,
	// so the 202 below honestly means "accepted".
	if err := normalize.Validate(body); err != nil {
		span.SetStatus(codes.Error, "malformed payload")
		recordIngestReceived(ctx, ingestresp.DropMalformed.Reason)
		ingestresp.WriteDropped(w, ingestresp.DropMalformed)
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
			recordIngestReceived(ctx, ingestresp.DropSpoolFull.Reason)
			ingestresp.WriteDropped(w, ingestresp.DropSpoolFull)
			return
		}
		recordIngestReceived(ctx, ingestresp.DropUnavailable.Reason)
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
		return
	}
	spoolSpan.End()

	recordIngestReceived(ctx, "accepted")
	ingestresp.WriteAccepted(w, record.IngestID)
}

func generateIngestID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000Z") + "-fallback"
	}

	return hex.EncodeToString(raw[:])
}
