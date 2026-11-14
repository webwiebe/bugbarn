package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/sqlitedb"
)

func wrapErr(err error, msg string) error {
	if err == nil {
		return nil
	}
	var classified *apperr.Error
	if errors.As(err, &classified) {
		// Already classified by a lower layer: keep its kind.
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound(msg, err)
	}
	if isUniqueViolation(err) {
		return apperr.Conflict(msg, err)
	}
	if sqlitedb.IsReadonlyRecovery(err) {
		return apperr.Unavailable(msg, err)
	}
	if sqlitedb.IsBusy(err) {
		return apperr.Busy(msg, err)
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
	if sqlitedb.IsReadonlyRecovery(err) {
		return apperr.Unavailable(msg, err)
	}
	if sqlitedb.IsBusy(err) {
		return apperr.Busy(msg, err)
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

// IsDatabaseLocked reports whether err is SQLite lock contention (SQLITE_BUSY
// or SQLITE_LOCKED). It matches an error storage already wrapped as
// apperr.ErrBusy and a raw driver error, by result code.
func IsDatabaseLocked(err error) bool {
	return errors.Is(err, apperr.ErrBusy) || sqlitedb.IsBusy(err)
}

// IsTransient reports whether a failed write is worth retrying: lock contention
// or a deadline. Storage caps batches with its own deadline and SQLite's busy
// handler ignores the context, so contention that outlasts the cap arrives as
// context.DeadlineExceeded. Every writer-side retry decision uses this one rule.
func IsTransient(err error) bool {
	return err != nil && (errors.Is(err, context.DeadlineExceeded) || IsDatabaseLocked(err))
}
