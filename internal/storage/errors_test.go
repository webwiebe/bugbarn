package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func TestWrapErr_NoRows(t *testing.T) {
	t.Parallel()

	err := wrapErr(sql.ErrNoRows, "not found")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestWrapErr_UniqueViolation(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("UNIQUE constraint failed: projects.slug")
	err := wrapErr(cause, "duplicate")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestWrapErr_Generic(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("connection refused")
	err := wrapErr(cause, "db error")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}

func TestWrapErr_Nil(t *testing.T) {
	t.Parallel()

	err := wrapErr(nil, "should not matter")
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestWrapNotFound_NoRows(t *testing.T) {
	t.Parallel()

	err := wrapNotFound(sql.ErrNoRows, "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestWrapNotFound_Other(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("timeout")
	err := wrapNotFound(cause, "db error")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}
