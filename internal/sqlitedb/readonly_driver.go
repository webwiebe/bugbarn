package sqlitedb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync"

	"modernc.org/sqlite"
)

// readonlyRetryBase is the name the reader pool's driver is registered under:
// the SQLite driver with every query retried on SQLITE_READONLY_RECOVERY.
const readonlyRetryBase = "sqlite-readonly-retry"

var (
	registerReadonlyOnce sync.Once
	readonlyDriver       = BaseDriverName
)

// ReadOnlyDriverName registers (once) the driver OpenReadOnly uses. A reader
// pod opens the -shm file read-only, so it cannot rebuild a wal-index header
// that needs recovery and the read fails with 264 until the writer has rebuilt
// it (#242, #261). Retrying in the driver covers every read on the reader
// pool, whichever store method or caller issues it.
func ReadOnlyDriverName() string {
	registerReadonlyOnce.Do(func() {
		sql.Register(readonlyRetryBase, readonlyRetryDriver{base: &sqlite.Driver{}})
		readonlyDriver = registerTracedOn(readonlyRetryBase, nil)
	})
	return readonlyDriver
}

type readonlyRetryDriver struct{ base driver.Driver }

func (d readonlyRetryDriver) Open(dsn string) (driver.Conn, error) {
	c, err := d.base.Open(dsn)
	if err != nil {
		return nil, err
	}
	return &readonlyRetryConn{Conn: c, walPath: walPathFromDSN(dsn)}, nil
}

// walPathFromDSN returns the -wal path for a file: DSN, for the span event the
// retry records.
func walPathFromDSN(dsn string) string {
	p := strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	return p + "-wal"
}

// readonlyRetryConn forwards every interface database/sql uses on the SQLite
// connection and retries queries and prepares on 264. SQLite reads the
// wal-index header when a read starts, which modernc does in the first step
// inside QueryContext, so a query that returned rows cannot hit 264 later.
type readonlyRetryConn struct {
	driver.Conn
	walPath string
}

func (c *readonlyRetryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	var rows driver.Rows
	err := retryReadonlyRecovery(ctx, c.walPath, func() error {
		var err error
		rows, err = q.QueryContext(ctx, query, args)
		return err
	})
	return rows, err
}

func (c *readonlyRetryConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var st driver.Stmt
	err := retryReadonlyRecovery(ctx, c.walPath, func() error {
		var err error
		if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
			st, err = p.PrepareContext(ctx, query)
		} else {
			st, err = c.Prepare(query)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &readonlyRetryStmt{Stmt: st, walPath: c.walPath}, nil
}

func (c *readonlyRetryConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *readonlyRetryConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin() //nolint:staticcheck // fallback for drivers without BeginTx
}

func (c *readonlyRetryConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *readonlyRetryConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *readonlyRetryConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// readonlyRetryStmt retries a prepared statement's query on 264, the same as
// readonlyRetryConn does for a direct query.
type readonlyRetryStmt struct {
	driver.Stmt
	walPath string
}

func (s *readonlyRetryStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	var rows driver.Rows
	err := retryReadonlyRecovery(ctx, s.walPath, func() error {
		var err error
		rows, err = q.QueryContext(ctx, args)
		return err
	})
	return rows, err
}

func (s *readonlyRetryStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := s.Stmt.(driver.StmtExecContext); ok {
		return e.ExecContext(ctx, args)
	}
	return nil, driver.ErrSkip
}
