package telemetrydb

import (
	"context"
	"errors"
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// ErrUnavailable is returned by a Source whose file did not open.
var ErrUnavailable = errors.New("telemetry database unavailable")

// Source hands out a telemetry file for reads. The writer wraps the handle it
// already has with Fixed; reader pods use Lazy.
type Source interface {
	DB(ctx context.Context) (*DB, error)
}

type fixed struct{ d *DB }

// Fixed wraps an open DB. A nil DB (a file that failed to open on the writer)
// answers ErrUnavailable.
func Fixed(d *DB) Source { return fixed{d: d} }

func (f fixed) DB(context.Context) (*DB, error) {
	if f.d == nil {
		return nil, ErrUnavailable
	}
	return f.d, nil
}

// Lazy opens a telemetry file read-only on first use, for reader pods. The
// writer creates the files, so a reader that starts first finds none; until
// then DB returns ErrNotCreated and the next call tries again. Once open, the
// handle is kept until Close.
type Lazy struct {
	spec Spec
	path string
	mu   sync.Mutex
	db   *DB
}

// NewLazy returns a Lazy for the file at path. It does not touch the disk.
func NewLazy(spec Spec, path string) *Lazy {
	return &Lazy{spec: spec, path: path}
}

// DB returns the read-only handle, opening it if needed.
func (l *Lazy) DB(ctx context.Context) (*DB, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db != nil {
		return l.db, nil
	}
	d, err := OpenReadOnly(ctx, l.spec, l.path)
	if errors.Is(err, ErrNotCreated) {
		return nil, err
	}
	if err != nil {
		return nil, apperr.Internal("open "+l.spec.Name+" db read-only", err)
	}
	l.db = d
	return d, nil
}

// Close closes the handle if it was opened.
func (l *Lazy) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return nil
	}
	err := l.db.Close()
	l.db = nil
	return err
}
