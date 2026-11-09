package storage

import (
	"context"
	"errors"
	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"testing"
)

var errReadonlyRecovery = errors.New("attempt to write a readonly database (264)")

func TestRetryReadonlyRecoveryReturnsAfterOneFailure(t *testing.T) {
	calls := 0
	err := retryReadonlyRecovery(context.Background(), "", func() error {
		calls++
		if calls == 1 {
			return errReadonlyRecovery
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil after one retry", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestRetryReadonlyRecoveryGivesUpAfterBackoffs(t *testing.T) {
	calls := 0
	err := retryReadonlyRecovery(context.Background(), "", func() error {
		calls++
		return errReadonlyRecovery
	})
	if !IsReadonlyRecovery(err) {
		t.Fatalf("err = %v, want it to wrap the 264 error", err)
	}
	if want := 1 + len(readonlyRecoveryBackoffs); calls != want {
		t.Fatalf("calls = %d, want %d", calls, want)
	}
}

func TestRetryReadonlyRecoveryLeavesOtherErrorsAlone(t *testing.T) {
	other := errors.New("disk I/O error")
	calls := 0
	err := retryReadonlyRecovery(context.Background(), "", func() error {
		calls++
		return other
	})
	if !errors.Is(err, other) || calls != 1 {
		t.Fatalf("err = %v, calls = %d, want the original error after one call", err, calls)
	}
}

// A reader whose wal-index cannot be initialised on a read-only mount fails with
// SQLITE_READONLY_CANTINIT (1288), the same transient state as 264. It was
// filed as bugbarn#268 (BS2-161) while only 264 was retried.
func TestRetryReadonlyRecoveryRetriesCantInit(t *testing.T) {
	cantInit := errors.New("attempt to write a readonly database (1288)")
	calls := 0
	err := retryReadonlyRecovery(context.Background(), "", func() error {
		calls++
		if calls == 1 {
			return cantInit
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil after one retry", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestWrapErrMapsCantInitToUnavailable(t *testing.T) {
	err := wrapNotFound(errors.New("attempt to write a readonly database (1288)"), "project not found")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err = %v, want Unavailable", err)
	}
}
