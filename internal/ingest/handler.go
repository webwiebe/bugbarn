package ingest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

const defaultQueueSize = 32768

type Handler struct {
	auth         *auth.Authorizer
	spool        *spool.Spool
	maxBodyBytes int64
	now          func() time.Time
	idFn         func() string
	queue        chan spool.Record
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
		queue:        make(chan spool.Record, defaultQueueSize),
	}
}

// Start drains the in-memory queue and flushes batches to the spool file.
// It returns when ctx is cancelled, flushing any remaining records first.
func (h *Handler) Start(ctx context.Context) {
	const maxBatch = 64
	batch := make([]spool.Record, 0, maxBatch)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := h.spool.AppendBatch(batch); err != nil {
			if !errors.Is(err, spool.ErrFull) {
				log.Printf("ingest: spool batch write: %v", err)
			}
		}
		batch = batch[:0]
	}

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case r := <-h.queue:
			batch = append(batch, r)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			// Drain whatever is left before exiting.
			for {
				select {
				case r := <-h.queue:
					batch = append(batch, r)
				default:
					flush()
					return
				}
			}
		}
	}
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
	return h.auth.ValidWithProject(r.Context(), r.Header.Get(auth.HeaderAPIKey))
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	select {
	case h.queue <- record:
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "ingest spool full", http.StatusTooManyRequests)
		return
	}

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
