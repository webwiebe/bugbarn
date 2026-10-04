// Package telemetrydb holds the SQLite files for infrastructure telemetry:
// security logs (security.db) and host metrics (metrics.db).
//
// They live next to the main database on the same volume but in their own
// files, each with its own single write connection. Security inserts are high
// volume and best-effort, so they must never queue behind event ingest on the
// main database's write connection, and the main database's 10GB+ migrations
// and retention sweeps must never see these tables. Do not ATTACH these files
// to the main database: an attached database breaks the main checkpoint (see
// internal/storage/checkpoint.go).
//
// Each file is hard-capped in size. The maintenance loop evicts the oldest
// rows once the file passes 90% of its cap, and inserts are refused once it
// reaches 100%, so a flood of logs can fill the cap but never the disk.
package telemetrydb

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/pressly/goose/v3"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

//go:embed migrations
var migrationFiles embed.FS

// ErrFull is returned by inserts while the file is at its size cap.
var ErrFull = errors.New("telemetry database is at its size cap")

// Spec describes one telemetry file: its migrations and the table the size
// cap evicts from.
type Spec struct {
	Name string
	// evictTable and evictCol name the bulk table and an indexed, increasing
	// column; eviction deletes the rows with the lowest values first.
	evictTable string
	evictCol   string
}

var (
	// Security is security.db: normalized security log rows.
	Security = Spec{Name: "security", evictTable: "security_logs", evictCol: "id"}
	// Metrics is metrics.db: per-minute host samples and hourly rollups.
	Metrics = Spec{Name: "metrics", evictTable: "samples_1m", evictCol: "ts"}
)

// DB is one telemetry file. A writer DB has both pools; a read-only DB (reader
// pods) has only the read pool and Write returns nil.
type DB struct {
	spec     Spec
	path     string
	maxBytes int64
	write    *sql.DB
	read     *sql.DB
	full     atomic.Bool
}

// Open opens (creating if needed) the file at path for writing and applies
// its migrations. maxBytes is the size cap; values <= 0 disable the cap.
func Open(ctx context.Context, spec Spec, path string, maxBytes int64) (*DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("%s db dir: %w", spec.Name, err)
	}
	w, err := sql.Open(storage.DriverName(), writeDSN(abs))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := migrate(ctx, w, spec); err != nil {
		_ = w.Close()
		return nil, err
	}
	r, err := sql.Open(storage.DriverName(), readDSN(abs))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	return &DB{spec: spec, path: abs, maxBytes: maxBytes, write: w, read: r}, nil
}

// OpenReadOnly opens an existing file read-only, for reader pods sharing the
// writer's volume. It fails while the writer has not created the file yet.
func OpenReadOnly(ctx context.Context, spec Spec, path string) (*DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("%s db not created yet: %w", spec.Name, err)
	}
	r, err := sql.Open(storage.DriverName(), readDSN(abs))
	if err != nil {
		return nil, err
	}
	r.SetMaxOpenConns(4)
	if err := r.PingContext(ctx); err != nil {
		_ = r.Close()
		return nil, err
	}
	return &DB{spec: spec, path: abs, read: r}, nil
}

// Write is the single write connection, nil on a read-only DB.
func (d *DB) Write() *sql.DB { return d.write }

// Read is the read pool.
func (d *DB) Read() *sql.DB { return d.read }

// Path is the absolute file path.
func (d *DB) Path() string { return d.path }

// Full reports whether the last maintenance pass found the file at its cap.
func (d *DB) Full() bool { return d.full.Load() }

// Close closes both pools.
func (d *DB) Close() error {
	var errs []error
	if d.write != nil {
		errs = append(errs, d.write.Close())
	}
	if d.read != nil {
		errs = append(errs, d.read.Close())
	}
	return errors.Join(errs...)
}

func migrate(ctx context.Context, db *sql.DB, spec Spec) error {
	sub, err := fs.Sub(migrationFiles, "migrations/"+spec.Name)
	if err != nil {
		return fmt.Errorf("%s migration fs: %w", spec.Name, err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("%s goose provider: %w", spec.Name, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("%s goose up: %w", spec.Name, err)
	}
	return nil
}

// writeDSN matches the main database's DSN (WAL, no autocheckpoint, immediate
// transactions) plus auto_vacuum=INCREMENTAL so eviction can hand pages back
// to the filesystem. auto_vacuum only takes effect on a file that has no
// tables yet, so it must come before anything creates one; on an existing file
// it is a no-op.
func writeDSN(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	params := strings.Join([]string{
		"mode=rwc",
		"_txlock=immediate",
		"_pragma=auto_vacuum(2)",
		"_pragma=busy_timeout(10000)",
		"_pragma=journal_mode(wal)",
		"_pragma=wal_autocheckpoint(0)",
		"_pragma=synchronous(normal)",
	}, "&")
	return u.String() + "?" + params
}

func readDSN(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String() + "?mode=ro&_pragma=busy_timeout(10000)"
}
