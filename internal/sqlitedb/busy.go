package sqlitedb

import (
	"errors"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// primaryCodeMask keeps the primary result code of an extended one: SQLite
// builds an extended code as primary | detail<<8.
const primaryCodeMask = 0xff

// IsBusy reports whether err is SQLite lock contention: SQLITE_BUSY or
// SQLITE_LOCKED, including their extended forms (SQLITE_BUSY_SNAPSHOT,
// SQLITE_BUSY_RECOVERY, SQLITE_LOCKED_SHAREDCACHE, ...). The condition is
// environmental, such as a checkpoint holding the file or a slow disk, and
// clears on its own, so callers may retry it.
//
// It classifies by the driver's result code only. An error that lost its
// *sqlite.Error on the way (flattened with %v, say) is not recognized.
func IsBusy(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() & primaryCodeMask {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	}
	return false
}
