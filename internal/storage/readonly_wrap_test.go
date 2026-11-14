package storage

import (
	"errors"
	"fmt"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

var errReadonlyRecovery = errors.New("attempt to write a readonly database (264)")

func TestWrapErrMapsCantInitToUnavailable(t *testing.T) {
	err := wrapNotFound(errors.New("attempt to write a readonly database (1288)"), "project not found")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err = %v, want Unavailable", err)
	}
}

func TestWrapErrMapsLockingProtocolToUnavailable(t *testing.T) {
	err := wrapNotFound(errors.New("locking protocol (15)"), "api key lookup")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err = %v, want Unavailable", err)
	}
}

func TestWrapErrMapsReadonlyRecoveryToUnavailable(t *testing.T) {
	err := fmt.Errorf("after 3 retries: %w", errReadonlyRecovery)
	for name, wrapped := range map[string]error{
		"wrapErr":      wrapErr(err, "x"),
		"wrapNotFound": wrapNotFound(err, "x"),
	} {
		if !errors.Is(wrapped, apperr.ErrUnavailable) {
			t.Errorf("%s: want apperr.ErrUnavailable, got %v", name, wrapped)
		}
	}
}
