package sqlitedb

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeDSN is the shape storage.sqliteDSN gives bugbarn.db: WAL with the
// built-in autocheckpoint off, 10s busy_timeout, immediate write transactions.
func writeDSN(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String() + "?mode=rwc&_txlock=immediate&_pragma=busy_timeout(10000)" +
		"&_pragma=journal_mode(wal)&_pragma=wal_autocheckpoint(0)&_pragma=synchronous(normal)"
}

// readDSN is the shape storage.sqliteReadOnlyDSN gives a reader pod.
func readDSN(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String() + "?mode=ro&_pragma=busy_timeout(10000)"
}

// openWriter opens path with one write connection and a projects table.
func openWriter(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open(DriverName(), writeDSN(path))
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE projects (
		id INTEGER PRIMARY KEY, slug TEXT NOT NULL UNIQUE, name TEXT, status TEXT, issue_prefix TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// openReader opens path read-only on the read-only recovery driver, as a reader
// pod does.
func openReader(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open(ReadOnlyDriverName(), readDSN(path))
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// growWAL writes enough rows to produce a non-trivial WAL. With
// wal_autocheckpoint(0) nothing reclaims it, so it only grows.
func growWAL(t *testing.T, db *sql.DB) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, err := db.Exec(
			`INSERT INTO projects (slug, name, status, issue_prefix) VALUES (?, ?, 'active', 'P')`,
			fmt.Sprintf("proj-%d", i), fmt.Sprintf("Project %d", i),
		); err != nil {
			t.Fatalf("seed write %d: %v", i, err)
		}
	}
}

func walSize(t *testing.T, dbPath string) int64 {
	t.Helper()
	fi, err := os.Stat(dbPath + "-wal")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("stat wal: %v", err)
	}
	return fi.Size()
}
