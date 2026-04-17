package storage

import (
	"context"
	"time"
)

// UpsertUser creates a user or updates their password if the username already exists.
func (s *Store) UpsertUser(ctx context.Context, username, passwordBcrypt string) error {
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO users (username, password_bcrypt, created_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(username) DO UPDATE SET password_bcrypt = excluded.password_bcrypt, updated_at = excluded.updated_at`,
		username, passwordBcrypt, now, now,
	)
	return err
}
