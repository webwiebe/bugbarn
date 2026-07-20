package storage

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	_ "modernc.org/sqlite"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

const (
	defaultDBPath  = ".data/bugbarn.db"
	defaultProject = "default"
	baseDriverName = "sqlite"
	issueIDPrefix  = "issue-"
	eventIDPrefix  = "event-"
	timeLayout     = time.RFC3339Nano
)

var (
	uuidPattern     = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	ipv4Pattern     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	longNumber      = regexp.MustCompile(`\b\d{4,}\b`)
	hexAddress      = regexp.MustCompile(`(?i)\b0x[0-9a-f]{6,}\b`)
	whitespace      = regexp.MustCompile(`\s+`)
	pathNumber      = regexp.MustCompile(`/\d+`)
	trimPunctuation = regexp.MustCompile(`^[\s:;,_\-]+|[\s:;,_\-]+$`)
)

var (
	registerDriverOnce sync.Once
	registeredDriver   = baseDriverName
)

// tracedDriver registers (once) an otelsql-wrapped SQLite driver and returns
// its name. Falls back to the plain driver if registration fails.
func tracedDriver() string {
	registerDriverOnce.Do(func() {
		name, err := otelsql.Register(baseDriverName,
			otelsql.WithAttributes(semconv.DBSystemSqlite),
			otelsql.WithSpanOptions(otelsql.SpanOptions{
				OmitConnResetSession: true,
				OmitConnPrepare:      true,
				OmitRows:             true,
			}),
			otelsql.WithMeterProvider(otel.GetMeterProvider()),
		)
		if err != nil {
			slog.Warn("otelsql driver registration failed; falling back to plain driver", "err", err)
			return
		}
		registeredDriver = name
	})
	return registeredDriver
}

// dbStatsRegs tracks the otelsql connection-pool metric registrations keyed
// by the *sql.DB they observe, so Close can unregister the callback and stop
// the gauges from reporting stats for a closed pool.
var (
	dbStatsRegsMu sync.Mutex
	dbStatsRegs   = map[*sql.DB]metric.Registration{}
)

// registerDBStats wires otelsql's observable connection-pool gauges
// (open/idle/in-use connections, wait counts, etc.) for db, tagged with a
// "pool" attribute so the read-only and read-write pools are distinguishable
// in exported metrics. Failures are logged, not fatal: metrics are best
// effort and must never block opening the database.
func registerDBStats(db *sql.DB, pool string) {
	reg, err := otelsql.RegisterDBStatsMetrics(db,
		otelsql.WithAttributes(semconv.DBSystemSqlite, attribute.String("pool", pool)),
		otelsql.WithMeterProvider(otel.GetMeterProvider()),
	)
	if err != nil {
		slog.Warn("otelsql db stats metrics registration failed", "pool", pool, "err", err)
		return
	}
	dbStatsRegsMu.Lock()
	dbStatsRegs[db] = reg
	dbStatsRegsMu.Unlock()
}

// unregisterDBStats stops the connection-pool gauges registered for db, if
// any were registered.
func unregisterDBStats(db *sql.DB) {
	dbStatsRegsMu.Lock()
	reg, ok := dbStatsRegs[db]
	if ok {
		delete(dbStatsRegs, db)
	}
	dbStatsRegsMu.Unlock()
	if ok {
		_ = reg.Unregister()
	}
}

// OpenReadOnly opens a read-only connection to an existing SQLite database.
// The returned Store has no write connection (db is nil) and skips schema
// initialisation and migrations — the database must already be set up.
// All read methods that go through readDB() work as normal; write methods
// will fail because db is nil.
func OpenReadOnly(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultDBPath
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	roDB, err := sql.Open(tracedDriver(), sqliteReadOnlyDSN(absPath))
	if err != nil {
		return nil, err
	}
	roDB.SetMaxOpenConns(4)
	registerDBStats(roDB, "read")

	// Verify the database is reachable.
	if err := roDB.Ping(); err != nil {
		unregisterDBStats(roDB)
		roDB.Close()
		return nil, err
	}

	return newStore(&core{db: nil, roDB: roDB}), nil
}

func Open(path string) (*Store, error) {
	return open(path, true)
}

// open is Open with control over the one-time background fingerprint migration.
// Tests that set explicit fingerprints pass autoMigrate=false: the migration
// recomputes fingerprints from event material, which would otherwise race the
// test and clobber the explicit values it just wrote.
func open(path string, autoMigrate bool) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultDBPath
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open(tracedDriver(), sqliteDSN(absPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	registerDBStats(db, "write")

	roDB, err := sql.Open(tracedDriver(), sqliteReadOnlyDSN(absPath))
	if err != nil {
		unregisterDBStats(db)
		db.Close()
		return nil, err
	}
	roDB.SetMaxOpenConns(4)
	registerDBStats(roDB, "read")

	c := &core{db: db, roDB: roDB}
	ctx := context.Background()
	if err := c.init(ctx); err != nil {
		unregisterDBStats(roDB)
		unregisterDBStats(db)
		roDB.Close()
		db.Close()
		return nil, err
	}
	if autoMigrate {
		go func() {
			if err := c.migrateFingerprints(context.Background()); err != nil {
				slog.Error("fingerprint migration failed", "err", err)
			}
		}()
	}
	return newStore(c), nil
}

func (s *core) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	if s.roDB != nil {
		unregisterDBStats(s.roDB)
		if err := s.roDB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.db != nil {
		unregisterDBStats(s.db)
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *core) readDB() *sql.DB {
	if s.roDB != nil {
		return s.roDB
	}
	return s.db
}

// DefaultProjectID returns the numeric ID of the default project.
func (s *core) DefaultProjectID() int64 {
	return s.defaultProjectID
}

// DB returns the underlying *sql.DB. Use sparingly — prefer Store methods.
func (s *core) DB() *sql.DB {
	return s.db
}

// PersistProcessedEvent stores a processed event and upserts the related issue.
// It returns the upserted issue, the stored event, a flag indicating whether this
// was a brand-new issue, a flag indicating whether the issue regressed, and any error.
func (s *core) PersistProcessedEvent(ctx context.Context, processed worker.ProcessedEvent) (Issue, Event, bool, bool, error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.PersistProcessedEvent",
		trace.WithAttributes(attribute.String("fingerprint", processed.Fingerprint)),
	)
	defer span.End()

	if s == nil || s.db == nil {
		span.SetStatus(codes.Error, "storage is nil")
		return Issue{}, Event{}, false, false, errors.New("storage is nil")
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}
	span.SetAttributes(attribute.Int64("project_id", projectID))

	_, upsertSpan := tracing.Tracer().Start(ctx, "storage.UpsertIssue")
	issue, issueID, regressed, err := s.upsertIssue(ctx, projectID, processed)
	if err != nil {
		upsertSpan.SetStatus(codes.Error, err.Error())
		upsertSpan.End()
		span.SetStatus(codes.Error, err.Error())
		return Issue{}, Event{}, false, false, err
	}
	upsertSpan.SetAttributes(attribute.String("issue_id", issue.ID), attribute.Bool("regressed", regressed))
	upsertSpan.End()

	// The upsert works entirely from the numeric project id, so the returned
	// issue carries no project slug. Populate it here so every consumer of the
	// published IssueCreated/IssueRegressed event — notably the cross-project
	// admin alert email, which spans all projects — can name the project.
	issue.ProjectSlug = s.projectSlugByID(ctx, projectID)

	if regressed {
		if _, rerr := s.db.ExecContext(ctx,
			`INSERT INTO regression_events (project_id, issue_id, regressed_at) VALUES (?, ?, ?)`,
			projectID, issueID, formatTime(issue.LastRegressedAt),
		); rerr != nil {
			slog.WarnContext(ctx, "storage: failed to record regression_event", "error", rerr)
		}
	}

	isNew := issue.EventCount == 1 && !regressed

	_, insertSpan := tracing.Tracer().Start(ctx, "storage.InsertEvent")
	eventRow, eventRowID, err := s.insertEvent(ctx, projectID, issueID, issue.ID, regressed, processed)
	if err != nil {
		insertSpan.SetStatus(codes.Error, err.Error())
		insertSpan.End()
		span.SetStatus(codes.Error, err.Error())
		return Issue{}, Event{}, false, false, err
	}
	insertSpan.End()

	_, facetSpan := tracing.Tracer().Start(ctx, "storage.InsertFacets")
	if err := s.insertFacets(ctx, projectID, issueID, eventRowID, processed.Event); err != nil {
		facetSpan.SetStatus(codes.Error, err.Error())
		facetSpan.End()
		span.SetStatus(codes.Error, err.Error())
		return Issue{}, Event{}, false, false, err
	}

	if err := s.PersistFacets(ctx, eventRowID, issueID, extractFacets(processed.Event)); err != nil {
		facetSpan.SetStatus(codes.Error, err.Error())
		facetSpan.End()
		span.SetStatus(codes.Error, err.Error())
		return Issue{}, Event{}, false, false, err
	}
	facetSpan.End()

	span.SetAttributes(attribute.Bool("is_new", isNew), attribute.Bool("regressed", regressed))
	return issue, eventRow, isNew, regressed, nil
}

// projectSlugByID returns the project's slug for a persisted issue. It is
// best-effort: a lookup failure yields an empty slug (and a warning) rather
// than failing the persist, since the slug is cosmetic to the write path and
// only decorates the published event. The lookup is a primary-key hit, so it
// adds negligible cost to ingest.
func (s *core) projectSlugByID(ctx context.Context, projectID int64) string {
	var slug string
	if err := s.readDB().QueryRowContext(ctx,
		`SELECT slug FROM projects WHERE id = ?`, projectID).Scan(&slug); err != nil {
		slog.WarnContext(ctx, "storage: failed to look up project slug for issue event",
			"project_id", projectID, "error", err)
		return ""
	}
	return slug
}
