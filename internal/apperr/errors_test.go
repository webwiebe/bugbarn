package apperr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func TestErrorIs(t *testing.T) {
	t.Parallel()

	err := apperr.NotFound("issue not found", fmt.Errorf("sql: no rows"))

	if !errors.Is(err, apperr.ErrNotFound) {
		t.Error("expected NotFound error to match ErrNotFound sentinel")
	}
	if errors.Is(err, apperr.ErrConflict) {
		t.Error("NotFound error should not match ErrConflict")
	}
	if errors.Is(err, apperr.ErrInvalidInput) {
		t.Error("NotFound error should not match ErrInvalidInput")
	}
}

func TestErrorAs(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("underlying db error")
	err := apperr.Internal("query failed", cause)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatal("expected errors.As to succeed")
	}
	if appErr.Code != "internal" {
		t.Errorf("code: got %q, want %q", appErr.Code, "internal")
	}
	if appErr.Message != "query failed" {
		t.Errorf("message: got %q, want %q", appErr.Message, "query failed")
	}
}

func TestErrorUnwrap(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("connection reset")
	err := apperr.Internal("db error", cause)

	if !errors.Is(err, cause) {
		t.Error("expected Unwrap to expose the underlying cause")
	}
}

func TestErrorString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *apperr.Error
		want string
	}{
		{"with cause", apperr.NotFound("issue", fmt.Errorf("no rows")), "not_found: issue: no rows"},
		{"without cause", apperr.InvalidInput("bad id", nil), "invalid_input: bad id"},
		{"sentinel", apperr.ErrConflict, "conflict"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWrappedInStandardError(t *testing.T) {
	t.Parallel()

	inner := apperr.NotFound("user", nil)
	wrapped := fmt.Errorf("service: %w", inner)

	if !errors.Is(wrapped, apperr.ErrNotFound) {
		t.Error("expected wrapped error to match ErrNotFound via errors.Is")
	}

	var appErr *apperr.Error
	if !errors.As(wrapped, &appErr) {
		t.Fatal("expected errors.As to find *apperr.Error through wrapping")
	}
	if appErr.Code != "not_found" {
		t.Errorf("code: got %q, want %q", appErr.Code, "not_found")
	}
}
