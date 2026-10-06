package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// autoVacuumIncremental is PRAGMA auto_vacuum's value for INCREMENTAL.
const autoVacuumIncremental = 2

// checkpointVacuumPages is how many freelist pages the writer's checkpoint loop
// hands back to the filesystem per tick (16 MiB at the default 4 KiB page,
// about 1 GiB an hour at the 60s interval). Only a database in
// auto_vacuum=INCREMENTAL mode has anything to return; on any other database
// PRAGMA incremental_vacuum does nothing.
const checkpointVacuumPages = 4096

// CompactResult reports what Compact did.
type CompactResult struct {
	// Compacted is true when the database was rewritten.
	Compacted bool
	// Reason says why nothing was done when Compacted is false.
	Reason      string
	BytesBefore int64
	BytesAfter  int64
	LiveBytes   int64
}

// Compact rewrites the database once in auto_vacuum=INCREMENTAL mode.
//
// bugbarn.db was created with auto_vacuum=NONE, so the pages the retention
// sweep frees stay in the file as freelist pages and the file never shrinks.
// On staging that left a 9 GB file around 45 MB of live data. A VACUUM is the
// only way to switch an existing file to INCREMENTAL; after it, the checkpoint
// loop returns freed pages to the filesystem on every tick.
//
// It does nothing on a file that is already INCREMENTAL, and refuses when the
// live data exceeds maxLiveBytes: VACUUM holds the single write connection for
// as long as it runs, and on a large database that is minutes of stalled
// ingest. Call it at writer start, before the workers take the connection.
func (s *core) Compact(ctx context.Context, path string, maxLiveBytes int64) (CompactResult, error) {
	var res CompactResult
	if s == nil || s.db == nil {
		res.Reason = "read-only store"
		return res, nil
	}
	mode, pageSize, pages, free, err := pageStats(ctx, s.db)
	if err != nil {
		return res, wrapErr(err, "read page stats")
	}
	res.BytesBefore = pages * pageSize
	res.LiveBytes = (pages - free) * pageSize
	if mode == autoVacuumIncremental {
		res.Reason = "already incremental"
		res.BytesAfter = res.BytesBefore
		return res, nil
	}
	if maxLiveBytes > 0 && res.LiveBytes > maxLiveBytes {
		res.Reason = fmt.Sprintf("live data %d bytes exceeds the %d byte limit", res.LiveBytes, maxLiveBytes)
		res.BytesAfter = res.BytesBefore
		return res, nil
	}

	// Fold the WAL into the file first so VACUUM starts from a short WAL.
	CheckpointDB(ctx, s.db, 0, nopLogger())
	if _, err := s.db.ExecContext(ctx, `PRAGMA main.auto_vacuum = INCREMENTAL`); err != nil {
		return res, wrapErr(err, "set auto_vacuum")
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM main`); err != nil {
		return res, wrapErr(err, "vacuum")
	}
	// In WAL mode VACUUM writes the new pages through the WAL; the file only
	// shrinks once a checkpoint copies them back and truncates it.
	CheckpointDB(ctx, s.db, 0, nopLogger())

	_, pageSize, pages, _, err = pageStats(ctx, s.db)
	if err != nil {
		return res, wrapErr(err, "read page stats")
	}
	res.Compacted = true
	res.BytesAfter = pages * pageSize
	if fi, err := os.Stat(path); err == nil {
		res.BytesAfter = fi.Size()
	}
	return res, nil
}

// incrementalVacuum returns up to pages freelist pages of db's main schema to
// the filesystem. It does nothing on a database that is not in
// auto_vacuum=INCREMENTAL mode (see Compact).
func incrementalVacuum(ctx context.Context, db *sql.DB, pages int, log *slog.Logger) {
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA main.incremental_vacuum(%d)`, pages)); err != nil && ctx.Err() == nil {
		log.Warn("incremental vacuum error", "error", err)
	}
}

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func pageStats(ctx context.Context, db *sql.DB) (mode, pageSize, pages, free int64, err error) {
	for _, p := range []struct {
		pragma string
		dst    *int64
	}{
		{"auto_vacuum", &mode},
		{"page_size", &pageSize},
		{"page_count", &pages},
		{"freelist_count", &free},
	} {
		if err = db.QueryRowContext(ctx, `PRAGMA main.`+p.pragma).Scan(p.dst); err != nil {
			return
		}
	}
	return
}

// RemoveLitestreamShadow deletes the shadow WAL directory Litestream kept next
// to the database (".<name>-litestream"). Litestream was removed from bugbarn,
// and nothing has cleaned that directory since: on testing it had grown to
// 16 GB of WAL segments that nothing reads. It reports the bytes removed and
// does nothing when the directory is absent.
func RemoveLitestreamShadow(dbPath string) (int64, error) {
	dir := filepath.Join(filepath.Dir(dbPath), "."+filepath.Base(dbPath)+"-litestream")
	fi, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, wrapErr(err, "stat litestream directory")
	}
	if !fi.IsDir() {
		return 0, nil
	}
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, ierr := d.Info(); ierr == nil {
				size += info.Size()
			}
		}
		return nil
	})
	if err := os.RemoveAll(dir); err != nil {
		return 0, wrapErr(err, "remove litestream directory")
	}
	return size, nil
}
