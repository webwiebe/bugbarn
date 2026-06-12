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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
		)
		if err != nil {
			slog.Warn("otelsql driver registration failed; falling back to plain driver", "err", err)
			return
		}
		registeredDriver = name
	})
	return registeredDriver
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

	// Verify the database is reachable.
	if err := roDB.Ping(); err != nil {
		roDB.Close()
		return nil, err
	}

	return &Store{db: nil, roDB: roDB}, nil
}

func Open(path string) (*Store, error) {
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

	roDB, err := sql.Open(tracedDriver(), sqliteReadOnlyDSN(absPath))
	if err != nil {
		db.Close()
		return nil, err
	}
	roDB.SetMaxOpenConns(4)

	store := &Store{db: db, roDB: roDB, path: absPath}
	store.checkpoints = newCheckpointMetrics(absPath + "-wal")
	ctx := context.Background()
	if err := store.init(ctx); err != nil {
		roDB.Close()
		db.Close()
		return nil, err
	}
	go func() {
		if err := store.migrateFingerprints(context.Background()); err != nil {
			slog.Error("fingerprint migration failed", "err", err)
		}
	}()
	return store, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	s.checkpoints.close()
	if s.roDB != nil {
		if err := s.roDB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Store) readDB() *sql.DB {
	if s.roDB != nil {
		return s.roDB
	}
	return s.db
}

// DefaultProjectID returns the numeric ID of the default project.
func (s *Store) DefaultProjectID() int64 {
	return s.defaultProjectID
}

// DB returns the underlying *sql.DB. Use sparingly — prefer Store methods.
func (s *Store) DB() *sql.DB {
	return s.db
}

// PersistProcessedEvent stores a processed event and upserts the related issue.
// It returns the upserted issue, the stored event, a flag indicating whether this
// was a brand-new issue, a flag indicating whether the issue regressed, and any error.
func (s *Store) PersistProcessedEvent(ctx context.Context, processed worker.ProcessedEvent) (Issue, Event, bool, bool, error) {
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
