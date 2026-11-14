package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/sqlitedb"
)

// driverBusyError returns the error SQLite gives a second writer while the
// first holds the write lock.
func driverBusyError(t *testing.T) error {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(t.TempDir(), "busy.db"))}
	dsn := u.String() + "?mode=rwc&_pragma=busy_timeout(20)&_pragma=journal_mode(wal)"
	ctx := context.Background()

	holder, err := sql.Open(sqlitedb.DriverName(), dsn)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`CREATE TABLE t (id INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.ExecContext(ctx, `ROLLBACK`) })

	other, err := sql.Open(sqlitedb.DriverName(), dsn)
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	t.Cleanup(func() { _ = other.Close() })
	_, err = other.Exec(`INSERT INTO t VALUES (1)`)
	if err == nil {
		t.Fatal("second writer succeeded while the first held the lock")
	}
	return err
}

func TestWrapErrMapsLockContentionToBusy(t *testing.T) {
	cause := driverBusyError(t)
	for name, wrap := range map[string]func(error, string) error{"wrapErr": wrapErr, "wrapNotFound": wrapNotFound} {
		err := wrap(fmt.Errorf("insert: %w", cause), "persist event")
		if !errors.Is(err, apperr.ErrBusy) {
			t.Errorf("%s: err = %v, want apperr.ErrBusy", name, err)
		}
		if errors.Is(err, apperr.ErrInternal) {
			t.Errorf("%s: busy must not also read as internal", name)
		}
		if !IsDatabaseLocked(err) || !IsTransient(err) {
			t.Errorf("%s: wrapped busy error not classified as locked and transient", name)
		}
	}
}

func TestIsDatabaseLockedByCodeNotText(t *testing.T) {
	cause := driverBusyError(t)
	if !IsDatabaseLocked(cause) {
		t.Error("raw driver busy error not recognized")
	}
	if !IsDatabaseLocked(apperr.Busy("persist", errors.New("anything"))) {
		t.Error("apperr.Busy not recognized")
	}
	if IsDatabaseLocked(errors.New("database is locked")) {
		t.Error("error text alone must not classify as locked")
	}
	if IsDatabaseLocked(nil) {
		t.Error("nil classified as locked")
	}
}

func TestIsTransient(t *testing.T) {
	t.Parallel()
	if IsTransient(nil) {
		t.Error("nil is not transient")
	}
	if !IsTransient(fmt.Errorf("persist: %w", context.DeadlineExceeded)) {
		t.Error("a deadline is transient")
	}
	if IsTransient(context.Canceled) {
		t.Error("a cancellation is not transient")
	}
	if IsTransient(apperr.Internal("x", errors.New("disk full"))) {
		t.Error("an internal error is not transient")
	}
}
