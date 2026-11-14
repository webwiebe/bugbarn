package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
)

// busyError returns the error SQLite gives a second writer while the first
// holds the write lock. The busy timeout is short so the wait ends fast; the
// error itself is the driver's own, not a built one.
func busyError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "busy.db")
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	dsn := u.String() + "?mode=rwc&_pragma=busy_timeout(20)&_pragma=journal_mode(wal)"

	holder, err := sql.Open(DriverName(), dsn)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`CREATE TABLE t (id INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	ctx := context.Background()
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("holder begin: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.ExecContext(ctx, `ROLLBACK`) })

	other, err := sql.Open(DriverName(), dsn)
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

func TestIsBusyRecognizesTheDriverResultCode(t *testing.T) {
	err := busyError(t)
	if !IsBusy(err) {
		t.Fatalf("IsBusy(%v) = false, want true", err)
	}
	if !IsBusy(fmt.Errorf("persist event: %w", err)) {
		t.Error("IsBusy must see through %w wrapping")
	}
}

func TestIsBusyIgnoresEverythingElse(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"nil":               nil,
		"plain":             errors.New("boom"),
		"deadline":          context.DeadlineExceeded,
		"no rows":           sql.ErrNoRows,
		"text only":         errors.New("database is locked (5) SQLITE_BUSY"),
		"flattened wrapper": fmt.Errorf("wrapped: %v", errors.New("SQLITE_BUSY")),
	} {
		if IsBusy(err) {
			t.Errorf("%s: IsBusy = true, want false (classification is by result code, not text)", name)
		}
	}
}
