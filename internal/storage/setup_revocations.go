package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// setupKeyNameSuffix ends the name of the api_keys row of a project's setup key.
const setupKeyNameSuffix = "-setup"

// RevokeSetupKey ends the setup key of slug for good: it records the revocation
// and removes the key's row, if there is one, in one transaction. Revoking a slug
// twice keeps the first time.
func (s *APIKeyStore) RevokeSetupKey(ctx context.Context, slug, keySHA256 string) error {
	if s.db == nil {
		return wrapErr(errors.New("no write connection"), "revoke setup key")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapErr(err, "revoke setup key: begin")
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO setup_key_revocations (slug, revoked_at) VALUES (?, ?)
ON CONFLICT(slug) DO NOTHING`, slug, formatTime(time.Now().UTC())); err != nil {
		return wrapErr(err, "revoke setup key")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_keys WHERE key_sha256 = ?`, keySHA256); err != nil {
		return wrapErr(err, "revoke setup key: delete row")
	}
	return wrapErr(tx.Commit(), "revoke setup key: commit")
}

// SetupKeyRevoked reports whether the setup key of slug was revoked. A reader can
// run this before the writer has migrated; nothing can be revoked until then, so
// a missing table answers false.
func (s *APIKeyStore) SetupKeyRevoked(ctx context.Context, slug string) (bool, error) {
	var found int
	err := retryReadonlyRecovery(ctx, s.walPath, func() error {
		row := s.readDB().QueryRowContext(ctx, `SELECT 1 FROM setup_key_revocations WHERE slug = ?`, slug)
		return row.Scan(&found)
	})
	if errors.Is(err, sql.ErrNoRows) || isMissingRevocationsTable(err) {
		return false, nil
	}
	if err != nil {
		return false, wrapErr(err, "read setup key revocation")
	}
	return true, nil
}

// ListSetupKeyRevocations returns every revoked setup key, newest first.
func (s *APIKeyStore) ListSetupKeyRevocations(ctx context.Context) ([]domain.SetupKeyRevocation, error) {
	rows, err := s.readDB().QueryContext(ctx, `
SELECT slug, revoked_at FROM setup_key_revocations ORDER BY revoked_at DESC, slug ASC`)
	if isMissingRevocationsTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, wrapErr(err, "list setup key revocations")
	}
	defer func() { _ = rows.Close() }()

	var out []domain.SetupKeyRevocation
	for rows.Next() {
		var rev domain.SetupKeyRevocation
		var revokedAt string
		if err := rows.Scan(&rev.Slug, &revokedAt); err != nil {
			return nil, wrapErr(err, "list setup key revocations: scan")
		}
		rev.RevokedAt, _ = parseTime(revokedAt)
		out = append(out, rev)
	}
	return out, wrapErr(rows.Err(), "list setup key revocations")
}

// APIKeyByID returns the API key row with this id, or apperr.NotFound.
func (s *APIKeyStore) APIKeyByID(ctx context.Context, id int64) (APIKey, error) {
	var k APIKey
	var createdAt string
	var lastUsedAt sql.NullString
	err := s.readDB().QueryRowContext(ctx, `
SELECT id, name, project_id, key_sha256, scope, created_at, last_used_at
FROM api_keys WHERE id = ?`, id).
		Scan(&k.ID, &k.Name, &k.ProjectID, &k.KeySHA256, &k.Scope, &createdAt, &lastUsedAt)
	if err != nil {
		return APIKey{}, wrapNotFound(err, "api key not found")
	}
	k.CreatedAt, _ = parseTime(createdAt)
	if lastUsedAt.Valid {
		k.LastUsedAt, _ = parseTime(lastUsedAt.String)
	}
	return k, nil
}

func isMissingRevocationsTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table: setup_key_revocations")
}
