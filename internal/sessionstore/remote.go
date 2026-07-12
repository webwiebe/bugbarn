package sessionstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Remote is the reader-replica Store. Plain validation reads hit the local
// read-only SQLite mount (WAL keeps them current), while every mutation —
// create, refresh, delete — is delegated to the writer's internal endpoints
// so refresh-token rotation happens exactly once, on the single writer.
type Remote struct {
	local     *storage.Store // read-only mount of the writer's database
	writerURL string
	secret    []byte
	client    *http.Client
	now       func() time.Time
}

// NewRemote builds a Remote store. local is the reader's read-only storage;
// writerURL and secret must match the writer's config.
func NewRemote(local *storage.Store, writerURL, secret string) *Remote {
	return &Remote{
		local:     local,
		writerURL: strings.TrimRight(writerURL, "/"),
		secret:    []byte(strings.TrimSpace(secret)),
		client:    &http.Client{Timeout: 15 * time.Second},
		now:       time.Now,
	}
}

// Get loads a session row from the local read-only SQLite.
func (r *Remote) Get(ctx context.Context, idHash string) (storage.WebSession, error) {
	ws, err := r.local.GetWebSession(ctx, idHash)
	if errors.Is(err, apperr.ErrNotFound) {
		return storage.WebSession{}, ErrNotFound
	}
	return ws, err
}

// Create persists a new session row via the writer.
func (r *Remote) Create(ctx context.Context, ws storage.WebSession) error {
	_, err := r.call(ctx, "create", Request{Session: &ws})
	return err
}

// Refresh delegates to the writer, whose singleflight guarantees the
// single-use refresh token is exchanged exactly once.
func (r *Remote) Refresh(ctx context.Context, idHash string) (storage.WebSession, error) {
	resp, err := r.call(ctx, "get-or-refresh", Request{IDHash: idHash})
	if err != nil {
		return storage.WebSession{}, err
	}
	if resp.Session == nil {
		return storage.WebSession{}, fmt.Errorf("%w: writer returned no session", ErrTransient)
	}
	if resp.Status == StatusTransient {
		return *resp.Session, fmt.Errorf("%w: %s", ErrTransient, resp.Error)
	}
	return *resp.Session, nil
}

// Delete removes a session row via the writer.
func (r *Remote) Delete(ctx context.Context, idHash string) error {
	_, err := r.call(ctx, "delete", Request{IDHash: idHash})
	return err
}

// DeleteBySID removes all sessions bound to an IdP session id via the writer.
func (r *Remote) DeleteBySID(ctx context.Context, sid string) (int64, error) {
	resp, err := r.call(ctx, "delete-by-sid", Request{SID: sid})
	if err != nil {
		return 0, err
	}
	return resp.Deleted, nil
}

// DeleteBySub removes all sessions for an IdP subject via the writer.
func (r *Remote) DeleteBySub(ctx context.Context, sub string) (int64, error) {
	resp, err := r.call(ctx, "delete-by-sub", Request{Sub: sub})
	if err != nil {
		return 0, err
	}
	return resp.Deleted, nil
}

// call signs and posts one internal request and maps the response status to
// the store's error taxonomy.
func (r *Remote) call(ctx context.Context, op string, payload Request) (Response, error) {
	payload.TS = r.now().Unix()
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.writerURL+InternalPathPrefix+op, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(AuthHeader, SignBody(r.secret, body))

	httpResp, err := r.client.Do(req)
	if err != nil {
		// Writer unreachable: transient — the middleware serves the stale
		// local row within the bounded grace window.
		return Response{}, fmt.Errorf("%w: %w", ErrTransient, err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))

	switch httpResp.StatusCode {
	case http.StatusOK:
		var resp Response
		if err := json.Unmarshal(raw, &resp); err != nil {
			return Response{}, fmt.Errorf("%w: decode writer response: %w", ErrTransient, err)
		}
		return resp, nil
	case http.StatusUnauthorized:
		return Response{}, ErrRevoked
	case http.StatusNotFound:
		return Response{}, ErrNotFound
	default:
		return Response{}, fmt.Errorf("%w: writer status %d", ErrTransient, httpResp.StatusCode)
	}
}
