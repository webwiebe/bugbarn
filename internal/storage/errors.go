package storage

import (
	"database/sql"
	"errors"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func wrapErr(err error, msg string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound(msg, err)
	}
	if isUniqueViolation(err) {
		return apperr.Conflict(msg, err)
	}
	return apperr.Internal(msg, err)
}

func wrapNotFound(err error, msg string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound(msg, err)
	}
	return apperr.Internal(msg, err)
}

func isUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		c := sqliteErr.Code()
		// SQLITE_CONSTRAINT (19) is the base code; SQLite also returns extended
		// codes like SQLITE_CONSTRAINT_UNIQUE (2067) for more specific violations.
		return c == sqlite3.SQLITE_CONSTRAINT || c == sqlite3.SQLITE_CONSTRAINT_UNIQUE
	}
	// Fallback for wrapped errors that carry the message string but not the typed error.
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// IsDatabaseLocked returns true when SQLite reports the database or a table is
// locked (SQLITE_BUSY or SQLITE_LOCKED). Used for retry logic on write contention.
func IsDatabaseLocked(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		c := sqliteErr.Code()
		return c == sqlite3.SQLITE_BUSY || c == sqlite3.SQLITE_LOCKED
	}
	// Fallback for wrapped errors.
	return strings.Contains(err.Error(), "database is locked")
}
