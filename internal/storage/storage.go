package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

const (
	defaultDBPath  = ".data/bugbarn.db"
	defaultProject = "default"
	driverName     = "sqlite"
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
	errNotFound = sql.ErrNoRows
	errConflict = errors.New("conflict")
)

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

	db, err := sql.Open(driverName, sqliteDSN(absPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DefaultProjectID returns the numeric ID of the default project.
func (s *Store) DefaultProjectID() int64 {
	return s.defaultProjectID
}

func (s *Store) PersistProcessedEvent(ctx context.Context, processed worker.ProcessedEvent) (Issue, Event, error) {
	if s == nil || s.db == nil {
		return Issue{}, Event{}, errors.New("storage is nil")
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	issue, issueID, regressed, err := s.upsertIssue(ctx, projectID, processed)
	if err != nil {
		return Issue{}, Event{}, err
	}

	eventRow, eventRowID, err := s.insertEvent(ctx, projectID, issueID, regressed, processed)
	if err != nil {
		return Issue{}, Event{}, err
	}

	if err := s.insertFacets(ctx, projectID, issueID, eventRowID, processed.Event); err != nil {
		return Issue{}, Event{}, err
	}

	if err := s.PersistFacets(ctx, eventRowID, issueID, extractFacets(processed.Event)); err != nil {
		return Issue{}, Event{}, err
	}

	return issue, eventRow, nil
}
