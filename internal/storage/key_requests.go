package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

var errKeyRequestReadOnly = apperr.Internal("storage is read-only", errors.New("no write connection"))

// CreateKeyRequest stores a pending request. pollSHA256 is the hash of the
// one-time poll secret returned to the requester.
func (s *KeyRequestStore) CreateKeyRequest(ctx context.Context, kr domain.KeyRequest, pollSHA256 string) error {
	if s == nil || s.db == nil {
		return errKeyRequestReadOnly
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO key_requests (id, project_id, label, poll_sha256, status, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		kr.ID, kr.ProjectID, kr.Label, pollSHA256, domain.KeyRequestPending,
		formatTime(kr.CreatedAt), formatTime(kr.ExpiresAt))
	return wrapErr(err, "create key request")
}

// CountPendingKeyRequests counts the open, unexpired requests of one project.
func (s *KeyRequestStore) CountPendingKeyRequests(ctx context.Context, projectID int64, now time.Time) (int, error) {
	var n int
	err := s.readDB().QueryRowContext(ctx, `
SELECT COUNT(*) FROM key_requests WHERE project_id = ? AND status = ? AND expires_at > ?`,
		projectID, domain.KeyRequestPending, formatTime(now)).Scan(&n)
	return n, wrapErr(err, "count pending key requests")
}

// GetKeyRequest loads a request and the hash of its poll secret.
func (s *KeyRequestStore) GetKeyRequest(ctx context.Context, id string) (domain.KeyRequest, string, error) {
	var kr domain.KeyRequest
	var pollSHA, created, expires string
	err := s.readDB().QueryRowContext(ctx, `
SELECT k.id, k.project_id, p.slug, k.label, k.status, k.poll_sha256, k.created_at, k.expires_at
FROM key_requests k JOIN projects p ON p.id = k.project_id WHERE k.id = ?`, id).
		Scan(&kr.ID, &kr.ProjectID, &kr.ProjectSlug, &kr.Label, &kr.Status, &pollSHA, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KeyRequest{}, "", apperr.NotFound("key request not found", err)
	}
	if err != nil {
		return domain.KeyRequest{}, "", wrapErr(err, "get key request")
	}
	kr.CreatedAt, _ = parseTime(created)
	kr.ExpiresAt, _ = parseTime(expires)
	return kr, pollSHA, nil
}

// ListPendingKeyRequests returns the open, unexpired requests of every project,
// oldest first.
func (s *KeyRequestStore) ListPendingKeyRequests(ctx context.Context, now time.Time) ([]domain.KeyRequest, error) {
	rows, err := s.readDB().QueryContext(ctx, `
SELECT k.id, k.project_id, p.slug, k.label, k.status, k.created_at, k.expires_at
FROM key_requests k JOIN projects p ON p.id = k.project_id
WHERE k.status = ? AND k.expires_at > ? ORDER BY k.created_at ASC`,
		domain.KeyRequestPending, formatTime(now))
	if err != nil {
		return nil, wrapErr(err, "list key requests")
	}
	defer rows.Close()
	var out []domain.KeyRequest
	for rows.Next() {
		var kr domain.KeyRequest
		var created, expires string
		if err := rows.Scan(&kr.ID, &kr.ProjectID, &kr.ProjectSlug, &kr.Label, &kr.Status, &created, &expires); err != nil {
			return nil, wrapErr(err, "scan key request")
		}
		kr.CreatedAt, _ = parseTime(created)
		kr.ExpiresAt, _ = parseTime(expires)
		out = append(out, kr)
	}
	return out, wrapErr(rows.Err(), "list key requests")
}

// ResolveKeyRequest moves a pending, unexpired request to approved or denied.
// It returns Conflict when the request is in any other state.
func (s *KeyRequestStore) ResolveKeyRequest(ctx context.Context, id, status string, now time.Time) error {
	if s == nil || s.db == nil {
		return errKeyRequestReadOnly
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE key_requests SET status = ?, resolved_at = ?
WHERE id = ? AND status = ? AND expires_at > ?`,
		status, formatTime(now), id, domain.KeyRequestPending, formatTime(now))
	if err != nil {
		return wrapErr(err, "resolve key request")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.Conflict("key request is not pending", nil)
	}
	return nil
}

// ClaimKeyRequest marks an approved, unexpired request claimed and stores the
// new key's hash in the same transaction, so a request yields at most one key.
// It returns Conflict when the request was not claimable.
func (s *KeyRequestStore) ClaimKeyRequest(ctx context.Context, id, keyName, keySHA256 string, now time.Time) error {
	if s == nil || s.db == nil {
		return errKeyRequestReadOnly
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapErr(err, "claim key request")
	}
	defer func() { _ = tx.Rollback() }()

	var projectID int64
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM key_requests WHERE id = ?`, id).Scan(&projectID); err != nil {
		return wrapNotFound(err, "claim key request")
	}
	res, err := tx.ExecContext(ctx, `
UPDATE key_requests SET status = ?, claimed_at = ?
WHERE id = ? AND status = ? AND expires_at > ?`,
		domain.KeyRequestClaimed, formatTime(now), id, domain.KeyRequestApproved, formatTime(now))
	if err != nil {
		return wrapErr(err, "claim key request")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.Conflict("key request is not claimable", nil)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO api_keys (name, project_id, key_sha256, scope, created_at) VALUES (?, ?, ?, ?, ?)`,
		keyName, projectID, keySHA256, domain.APIKeyScopeIngest, formatTime(now)); err != nil {
		return wrapErr(err, "claim key request")
	}
	return wrapErr(tx.Commit(), "claim key request")
}

// PurgeKeyRequests deletes requests that expired before the cutoff.
func (s *KeyRequestStore) PurgeKeyRequests(ctx context.Context, cutoff time.Time) error {
	if s == nil || s.db == nil {
		return errKeyRequestReadOnly
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM key_requests WHERE expires_at < ?`, formatTime(cutoff))
	return wrapErr(err, "purge key requests")
}
