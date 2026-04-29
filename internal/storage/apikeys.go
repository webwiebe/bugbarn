package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CreateAPIKey stores an API key's SHA-256 hash and returns the resulting row.
// scope must be APIKeyScopeFull or APIKeyScopeIngest.
func (s *Store) CreateAPIKey(ctx context.Context, name string, projectID int64, keySHA256, scope string) (APIKey, error) {
	if scope != APIKeyScopeFull && scope != APIKeyScopeIngest {
		scope = APIKeyScopeFull
	}
	now := formatTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO api_keys (name, project_id, key_sha256, scope, created_at) VALUES (?, ?, ?, ?, ?)`,
		name, projectID, keySHA256, scope, now,
	)
	if err != nil {
		return APIKey{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return APIKey{}, err
	}
	return APIKey{ID: id, Name: name, ProjectID: projectID, KeySHA256: keySHA256, Scope: scope, CreatedAt: time.Now().UTC()}, nil
}

// ListAPIKeys returns all API key rows (without the plaintext key).
func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, project_id, key_sha256, scope, created_at, last_used_at
FROM api_keys ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var createdAt string
		var lastUsedAt sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.ProjectID, &k.KeySHA256, &k.Scope, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		k.CreatedAt, _ = parseTime(createdAt)
		if lastUsedAt.Valid {
			k.LastUsedAt, _ = parseTime(lastUsedAt.String)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// DeleteAPIKey removes the API key with the given id.
func (s *Store) DeleteAPIKey(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TouchAPIKey updates last_used_at for the key matching the given SHA-256 hex.
func (s *Store) TouchAPIKey(ctx context.Context, keySHA256 string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE api_keys SET last_used_at = ? WHERE key_sha256 = ?`,
		formatTime(time.Now().UTC()), keySHA256,
	)
	return err
}

// EnsureSetupAPIKey creates the API key if no key with the same sha256 exists yet (idempotent).
func (s *Store) EnsureSetupAPIKey(ctx context.Context, name string, projectID int64, keySHA256 string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO api_keys (name, project_id, key_sha256, scope, created_at)
VALUES (?, ?, ?, 'ingest', ?)
ON CONFLICT(key_sha256) DO NOTHING`,
		name, projectID, keySHA256, formatTime(time.Now().UTC()),
	)
	return err
}

// ValidAPIKeySHA256 returns the project_id and scope for the API key matching the given SHA-256 hex digest.
// Returns (0, "", false, nil) when no matching key exists.
func (s *Store) ValidAPIKeySHA256(ctx context.Context, keySHA256 string) (projectID int64, scope string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT project_id, scope FROM api_keys WHERE key_sha256 = ?`, keySHA256).Scan(&projectID, &scope)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return projectID, scope, true, nil
}
