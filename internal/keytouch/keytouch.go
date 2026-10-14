// Package keytouch carries "this API key was just used" from the reader pods to
// the writer. Readers open the database read-only, so they cannot update
// api_keys.last_used_at themselves; they collect touches in memory and send one
// signed batch to the writer on an interval.
package keytouch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
)

// Path is the writer-internal endpoint that applies a batch of touches. It is
// signed like the session endpoints: an HMAC over the body with the shared
// session secret, and a timestamp inside the body that bounds replay.
const Path = "/internal/v1/api-key-touch"

// FlushInterval is how often a reader sends what it collected. last_used_at is
// advisory, so a coarse interval costs nothing and keeps the writer's single
// write connection free.
const FlushInterval = 30 * time.Second

// maxPending bounds the in-memory map when the writer stays unreachable. Only
// distinct keys count, so this is far above any real deployment.
const maxPending = 10000

// Batch is the signed body of one flush.
type Batch struct {
	TS      int64                 `json:"ts"`
	Touches []domain.APIKeyTouch `json:"touches"`
}

// Sender delivers one batch to the writer.
type Sender func(ctx context.Context, touches []domain.APIKeyTouch) error

// Batcher collects touches and flushes them. Safe for concurrent use.
type Batcher struct {
	send Sender
	now  func() time.Time
	log  *slog.Logger

	mu      sync.Mutex
	pending map[string]time.Time
}

// NewBatcher returns a batcher that delivers through send.
func NewBatcher(send Sender, log *slog.Logger) *Batcher {
	return &Batcher{send: send, now: time.Now, log: log, pending: map[string]time.Time{}}
}

// Touch records a use of the key with this SHA-256. It never blocks on the
// writer and never fails, so it fits the auth path's touch hook.
func (b *Batcher) Touch(_ context.Context, keySHA256 string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, known := b.pending[keySHA256]; !known && len(b.pending) >= maxPending {
		return nil
	}
	b.pending[keySHA256] = b.now().UTC()
	return nil
}

// Flush sends everything collected so far. A failed send puts the touches back,
// keeping the newer time when the key was used again in the meantime.
func (b *Batcher) Flush(ctx context.Context) error {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch := make([]domain.APIKeyTouch, 0, len(b.pending))
	for sha, at := range b.pending {
		batch = append(batch, domain.APIKeyTouch{KeySHA256: sha, At: at})
	}
	b.pending = map[string]time.Time{}
	b.mu.Unlock()

	if err := b.send(ctx, batch); err != nil {
		b.mu.Lock()
		for _, t := range batch {
			if cur, ok := b.pending[t.KeySHA256]; !ok || cur.Before(t.At) {
				if !ok && len(b.pending) >= maxPending {
					continue
				}
				b.pending[t.KeySHA256] = t.At
			}
		}
		b.mu.Unlock()
		return err
	}
	return nil
}

// Run flushes every FlushInterval until ctx ends, then once more so a clean
// shutdown loses nothing. A failed flush logs at Warn: last_used_at is advisory
// and the next tick retries.
func (b *Batcher) Run(ctx context.Context) {
	t := time.NewTicker(FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = b.Flush(final)
			cancel()
			return
		case <-t.C:
			if err := b.Flush(ctx); err != nil && ctx.Err() == nil {
				b.log.Warn("api key last-used flush failed", "error", err)
			}
		}
	}
}

// NewRemoteSender returns a Sender that posts signed batches to the writer.
func NewRemoteSender(writerURL, secret string) Sender {
	client := &http.Client{Timeout: 10 * time.Second}
	base := strings.TrimRight(writerURL, "/")
	key := []byte(secret)
	return func(ctx context.Context, touches []domain.APIKeyTouch) error {
		body, err := json.Marshal(Batch{TS: time.Now().Unix(), Touches: touches})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+Path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(sessionstore.AuthHeader, sessionstore.SignBody(key, body))
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("writer answered %d", resp.StatusCode)
		}
		return nil
	}
}
