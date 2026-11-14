package storage

import (
	"net/url"
	"path/filepath"
	"strings"
)

func sqliteDSN(path string) string {
	u := url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}
	// wal_autocheckpoint(0) disables SQLite's built-in autocheckpoint; the
	// writer's RunPeriodicCheckpoint loop is the SOLE checkpointer instead.
	// SQLite's automatic checkpoint is PASSIVE, and a PASSIVE checkpoint stops
	// at the first reader snapshot boundary — with reader pods holding
	// snapshots on this file continuously there is no quiet window, so it can
	// never bound WAL growth. RunPeriodicCheckpoint issues TRUNCATE with retry,
	// which can. Keep exactly one checkpointer: two racing for the write lock
	// just spam "database is locked".
	params := strings.Join([]string{
		"mode=rwc",
		"_txlock=immediate",
		"_pragma=busy_timeout(10000)",
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(wal)",
		"_pragma=wal_autocheckpoint(0)",
		"_pragma=synchronous(normal)",
	}, "&")
	return u.String() + "?" + params
}

func sqliteReadOnlyDSN(path string) string {
	u := url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}
	return u.String() + "?mode=ro&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)"
}
