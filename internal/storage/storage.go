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

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

const (
	defaultDBPath  = ".data/bugbarn.db"
	defaultProject = "default"
	baseDriverName = "sqlite"
	issueIDPrefix  = "issue-"
	eventIDPrefix  = "event-"
	// timeLayout writes every stored timestamp with a nine-digit fraction.
	// SQLite compares these columns as text, and RFC3339Nano trims trailing
	// zeros, so inside one second "…:55.12Z" sorted after "…:55.123Z" (#253).
	// A fixed width makes text order equal time order.
	timeLayout = "2006-01-02T15:04:05.000000000Z07:00"
	// parseLayout reads both the fixed-width form and the trimmed form that
	// rows written before #253 still carry.
	parseLayout = time.RFC3339Nano
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

	registerBudgetedOnce sync.Once
	budgetedDriver       = baseDriverName
)

// registerTraced registers an otelsql-wrapped SQLite driver and returns its
// name. Falls back to the plain driver if registration fails. recordError
// decides which errors mark a span as failed; nil records all of them.
func registerTraced(recordError func(error) bool) string {
	return registerTracedOn(baseDriverName, recordError)
}

// registerTracedOn is registerTraced over the registered driver base.
func registerTracedOn(base string, recordError func(error) bool) string {
	name, err := otelsql.Register(base,
		otelsql.WithAttributes(semconv.DBSystemSqlite),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			OmitConnResetSession: true,
			OmitConnPrepare:      true,
			OmitRows:             true,
			RecordError:          recordError,
		}),
		otelsql.WithMeterProvider(otel.GetMeterProvider()),
	)
	if err != nil {
		slog.Warn("otelsql driver registration failed; falling back to plain driver", "err", err)
		return base
	}
	return name
}

// tracedDriver registers (once) the driver the main database uses.
func tracedDriver() string {
	registerDriverOnce.Do(func() { registeredDriver = registerTraced(nil) })
	return registeredDriver
}

// DriverName returns the otelsql-wrapped SQLite driver name, so other SQLite
// files (internal/telemetrydb) get the same tracing as the main database.
func DriverName() string { return tracedDriver() }

// BudgetedDriverName returns a driver with the same tracing as DriverName,
// except that an expired context deadline does not mark the span as failed.
// It is for handles whose work runs under a deadline that ends it on purpose,
// such as the telemetry maintenance pass, where every expiry would otherwise
// count as an error trace. The span keeps its statement and duration, and the
// pass still logs what it could not finish.
func BudgetedDriverName() string {
	registerBudgetedOnce.Do(func() {
		budgetedDriver = registerTraced(func(err error) bool {
			return !errors.Is(err, context.DeadlineExceeded)
		})
	})
	return budgetedDriver
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
	return openReadOnlyDSN(sqliteReadOnlyDSN(absPath))
}

// openReadOnlyDSN is OpenReadOnly on a full DSN. Tests add readonly_shm=1, the
// state a reader pod is in because its volume is mounted read-only.
func openReadOnlyDSN(dsn string) (*Store, error) {
	roDB, err := sql.Open(readonlyTracedDriver(), dsn)
	if err != nil {
		return nil, err
	}
	roDB.SetMaxOpenConns(4)
	registerDBStats(roDB, "read")

	// Verify the database is reachable.
	if err := roDB.Ping(); err != nil {
		unregisterDBStats(roDB)
		_ = roDB.Close()
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
		_ = db.Close()
		return nil, err
	}
	roDB.SetMaxOpenConns(4)
	registerDBStats(roDB, "read")

	c := &core{
		db: db, roDB: roDB,
		mergeFingerprintCollisions: os.Getenv("BUGBARN_FINGERPRINT_MERGE") == "true",
	}
	ctx := context.Background()
	if err := c.init(ctx); err != nil {
		unregisterDBStats(roDB)
		unregisterDBStats(db)
		_ = roDB.Close()
		_ = db.Close()
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
func (s *core) PersistProcessedEvent(ctx context.Context, processed event.ProcessedEvent) (Issue, Event, bool, bool, error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.PersistProcessedEvent",
		trace.WithAttributes(attribute.String("fingerprint", processed.Fingerprint)),
	)
	defer span.End()

	if s == nil || s.db == nil {
		span.SetStatus(codes.Error, "storage is nil")
		return Issue{}, Event{}, false, false, errors.New("storage is nil")
	}

	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}
	span.SetAttributes(attribute.Int64("project_id", projectID))

	issue, issueID, regressed, err := s.upsertIssueTraced(ctx, projectID, processed)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return Issue{}, Event{}, false, false, err
	}

	// The upsert works entirely from the numeric project id, so the returned
	// issue carries no project slug. Populate it here so every consumer of the
	// published IssueCreated/IssueRegressed event — notably the cross-project
	// admin alert email, which spans all projects — can name the project.
	issue.ProjectSlug = s.projectSlugByID(ctx, projectID)

	if regressed {
		s.recordRegression(ctx, projectID, issueID, issue.LastRegressedAt)
	}

	isNew := issue.EventCount == 1 && !regressed

	// Sampling decision. issue.EventCount was incremented by the upsert above and
	// is the issue's lifetime total, so it is the only input the ladder needs —
	// no extra query, no counter, no coordination. Skipping the event row leaves
	// the issue itself untouched: the count stays exact, alerts read it rather
	// than counting rows, the regression row is already written, and the facet
	// projection has not depended on an event row since migration 00013. The
	// issue also keeps representative_event_json, so there is always a full
	// example payload to look at.
	keep, weight := sampleFor(int64(issue.EventCount), s.sampleAfterFor(ctx, projectID))
	if !keep {
		span.SetAttributes(attribute.Bool("sampled_out", true))
		if err := s.persistFacetsFor(ctx, projectID, issueID, processed.Event); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return Issue{}, Event{}, false, false, err
		}
		span.SetAttributes(attribute.Bool("is_new", isNew), attribute.Bool("regressed", regressed))
		return issue, Event{}, isNew, regressed, nil
	}

	_, insertSpan := tracing.Tracer().Start(ctx, "storage.InsertEvent")
	eventRow, err := s.insertEvent(ctx, projectID, issueID, issue.ID, regressed, weight, processed)
	if err != nil {
		insertSpan.SetStatus(codes.Error, err.Error())
		insertSpan.End()
		span.SetStatus(codes.Error, err.Error())
		return Issue{}, Event{}, false, false, err
	}
	insertSpan.End()

	if err := s.persistFacetsFor(ctx, projectID, issueID, processed.Event); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return Issue{}, Event{}, false, false, err
	}

	span.SetAttributes(attribute.Bool("is_new", isNew), attribute.Bool("regressed", regressed))
	return issue, eventRow, isNew, regressed, nil
}

// upsertIssueTraced runs upsertIssue under its own span.
func (s *core) upsertIssueTraced(ctx context.Context, projectID int64, processed event.ProcessedEvent) (Issue, int64, bool, error) {
	_, span := tracing.Tracer().Start(ctx, "storage.UpsertIssue")
	defer span.End()
	issue, issueID, regressed, err := s.upsertIssue(ctx, projectID, processed)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return Issue{}, 0, false, err
	}
	span.SetAttributes(attribute.String("issue_id", issue.ID), attribute.Bool("regressed", regressed))
	return issue, issueID, regressed, nil
}

// recordRegression writes the regression_events row. It is best-effort: a
// failure is logged and does not fail the persist.
func (s *core) recordRegression(ctx context.Context, projectID, issueID int64, at time.Time) {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO regression_events (project_id, issue_id, regressed_at) VALUES (?, ?, ?)`,
		projectID, issueID, formatTime(at),
	); err != nil {
		slog.WarnContext(ctx, "storage: failed to record regression_event", "error", err)
	}
}

// persistFacetsFor writes both facet projections for an issue: the flattened
// payload and the curated key set. It runs whether or not the event itself was
// stored — a sampled-out event still tells us the issue was seen on this host,
// in this environment, on this release, and that is exactly the kind of fact the
// projection exists to answer.
func (s *core) persistFacetsFor(ctx context.Context, projectID, issueID int64, evt event.Event) error {
	_, facetSpan := tracing.Tracer().Start(ctx, "storage.InsertFacets")
	defer facetSpan.End()

	if err := s.insertFacets(ctx, projectID, issueID, evt); err != nil {
		facetSpan.SetStatus(codes.Error, err.Error())
		return err
	}
	if err := s.PersistFacets(ctx, issueID, extractFacets(evt)); err != nil {
		facetSpan.SetStatus(codes.Error, err.Error())
		return err
	}
	s.ensureRelease(ctx, projectID, evt)
	return nil
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
