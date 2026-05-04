package storage

import (
	"database/sql"
	"errors"
	"strings"

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
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
