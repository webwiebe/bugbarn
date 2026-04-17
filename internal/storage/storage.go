package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wiebe-xyz/bugbarn/internal/event"
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

type Store struct {
	db               *sql.DB
	defaultProjectID int64
}

type ctxProjectKey struct{}

// WithProjectID returns a context carrying the given project ID for use by Store methods.
func WithProjectID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxProjectKey{}, id)
}

// ProjectIDFromContext extracts the project ID stored by WithProjectID.
// Returns (id, true) when a positive project ID is present, (0, false) otherwise.
func ProjectIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxProjectKey{}).(int64)
	return id, ok && id > 0
}

type Issue struct {
	ID                     string
	Fingerprint            string
	FingerprintMaterial    string
	FingerprintExplanation []string
	Title                  string
	NormalizedTitle        string
	ExceptionType          string
	Status                 string
	ResolvedAt             time.Time
	ReopenedAt             time.Time
	LastRegressedAt        time.Time
	RegressionCount        int
	FirstSeen              time.Time
	LastSeen               time.Time
	EventCount             int
	RepresentativeEvent    event.Event
}

type Event struct {
	ID                     string
	IssueID                string
	Fingerprint            string
	FingerprintMaterial    string
	FingerprintExplanation []string
	ReceivedAt             time.Time
	ObservedAt             time.Time
	Severity               string
	Message                string
	Regressed              bool
	Payload                event.Event
}

type Release struct {
	ID          string
	Name        string
	Environment string
	ObservedAt  time.Time
	Version     string
	CommitSHA   string
	URL         string
	Notes       string
	CreatedBy   string
	CreatedAt   time.Time
}

type Alert struct {
	ID        string
	Name      string
	Enabled   bool
	Severity  string
	Rule      map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
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

// IssueFilter holds optional filters and sort order for ListIssuesFiltered.
type IssueFilter struct {
	// Sort is one of "last_seen", "first_seen", "event_count". Default: "last_seen".
	Sort string
	// Status filters by issue status: "open" maps to "unresolved", "resolved" to "resolved".
	// Empty string means no filter (all issues).
	Status string
	// Query is a case-insensitive substring matched against title and normalized_title.
	Query string
	// Facets is an optional map of facet key→value pairs to filter by.
	// Issues must match ALL provided facet filters (AND semantics).
	Facets map[string]string
}

func (s *Store) ListIssues(ctx context.Context) ([]Issue, error) {
	return s.ListIssuesFiltered(ctx, IssueFilter{})
}

func (s *Store) ListIssuesFiltered(ctx context.Context, filter IssueFilter) ([]Issue, error) {
	var orderBy string
	switch filter.Sort {
	case "first_seen":
		orderBy = "first_seen DESC, id DESC"
	case "event_count":
		orderBy = "event_count DESC, id DESC"
	default:
		orderBy = "last_seen DESC, id DESC"
	}

	// Collect non-empty facet filters.
	type kv struct{ k, v string }
	var facetFilters []kv
	for fk, fv := range filter.Facets {
		if strings.TrimSpace(fk) != "" && strings.TrimSpace(fv) != "" {
			facetFilters = append(facetFilters, kv{fk, fv})
		}
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	conditions := []string{"i.project_id = ?"}
	whereArgs := []any{projectID}

	switch filter.Status {
	case "open":
		conditions = append(conditions, "i.status = 'unresolved'")
	case "resolved":
		conditions = append(conditions, "i.status = 'resolved'")
	}

	if q := strings.TrimSpace(filter.Query); q != "" {
		conditions = append(conditions, "(i.title LIKE ? OR i.normalized_title LIKE ?)")
		like := "%" + q + "%"
		whereArgs = append(whereArgs, like, like)
	}

	// fromArgs holds bindings for subquery ?-placeholders in the FROM clause.
	// They must come before whereArgs in the final args slice.
	var fromArgs []any
	var fromClause string
	if len(facetFilters) > 0 {
		// Build an INTERSECT subquery: one branch per facet filter that returns
		// matching issue_ids. The join enforces AND semantics across all filters.
		var subqueries []string
		for _, f := range facetFilters {
			subqueries = append(subqueries,
				`SELECT DISTINCT issue_id FROM event_facets WHERE project_id = ? AND facet_key = ? AND facet_value = ?`)
			fromArgs = append(fromArgs, projectID, f.k, f.v)
		}
		fromClause = fmt.Sprintf(`issues i INNER JOIN (%s) ef ON i.id = ef.issue_id`,
			strings.Join(subqueries, " INTERSECT "))
	} else {
		fromClause = "issues i"
	}

	// Combine: subquery bindings first (appear in FROM clause), then WHERE bindings.
	args := append(fromArgs, whereArgs...)

	sqlQuery := `
SELECT
	i.id,
	i.fingerprint,
	i.fingerprint_material,
	i.fingerprint_explanation_json,
	i.title,
	i.normalized_title,
	i.exception_type,
	i.status,
	i.resolved_at,
	i.reopened_at,
	i.last_regressed_at,
	i.regression_count,
	i.first_seen,
	i.last_seen,
	i.event_count,
	i.representative_event_json
FROM ` + fromClause + `
WHERE ` + strings.Join(conditions, " AND ") + `
ORDER BY i.` + orderBy

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var issues []Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return issues, nil
}

func (s *Store) GetIssue(ctx context.Context, issueID string) (Issue, error) {
	rowID, err := parseID(issueIDPrefix, issueID)
	if err != nil {
		return Issue{}, err
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	row := s.db.QueryRowContext(ctx, `
SELECT
	id,
	fingerprint,
	fingerprint_material,
	fingerprint_explanation_json,
	title,
	normalized_title,
	exception_type,
	status,
	resolved_at,
	reopened_at,
	last_regressed_at,
	regression_count,
	first_seen,
	last_seen,
	event_count,
	representative_event_json
FROM issues
WHERE project_id = ? AND id = ?`,
		projectID,
		rowID,
	)

	return scanIssue(row)
}

// ListIssueEventsPage returns up to limit events for the given issue, ordered
// newest-first. If beforeID is non-zero only events with id < beforeID are
// returned, enabling cursor-based pagination. Results are reversed before
// returning so callers always receive events in ascending (oldest-first) order
// within the page.
func (s *Store) ListIssueEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]Event, bool, error) {
	rowID, err := parseID(issueIDPrefix, issueID)
	if err != nil {
		return nil, false, err
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	if limit <= 0 {
		limit = 25
	}

	// Fetch one extra row to know whether there are more pages.
	fetch := limit + 1

	var rows *sql.Rows
	if beforeID > 0 {
		rows, err = s.db.QueryContext(ctx, `
SELECT id, issue_id, fingerprint, fingerprint_material, fingerprint_explanation_json,
       received_at, observed_at, severity, message, regressed, event_json
FROM events
WHERE project_id = ? AND issue_id = ? AND id < ?
ORDER BY id DESC LIMIT ?`,
			projectID, rowID, beforeID, fetch)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT id, issue_id, fingerprint, fingerprint_material, fingerprint_explanation_json,
       received_at, observed_at, severity, message, regressed, event_json
FROM events
WHERE project_id = ? AND issue_id = ?
ORDER BY id DESC LIMIT ?`,
			projectID, rowID, fetch)
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		entry, err := scanEvent(rows)
		if err != nil {
			return nil, false, err
		}
		events = append(events, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}

	// Reverse to ascending order for display.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	return events, hasMore, nil
}

func (s *Store) GetEvent(ctx context.Context, eventID string) (Event, error) {
	rowID, err := parseID(eventIDPrefix, eventID)
	if err != nil {
		return Event{}, err
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	row := s.db.QueryRowContext(ctx, `
SELECT
	id,
	issue_id,
	fingerprint,
	fingerprint_material,
	fingerprint_explanation_json,
	received_at,
	observed_at,
	severity,
	message,
	regressed,
	event_json
FROM events
WHERE project_id = ? AND id = ?`,
		projectID,
		rowID,
	)

	return scanEvent(row)
}

func (s *Store) ListRecentEvents(ctx context.Context, limit int, since time.Time) ([]Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-15 * time.Minute)
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
	id,
	issue_id,
	fingerprint,
	fingerprint_material,
	fingerprint_explanation_json,
	received_at,
	observed_at,
	severity,
	message,
	regressed,
	event_json
FROM events
WHERE project_id = ? AND max(received_at, observed_at) >= ?
ORDER BY max(received_at, observed_at) DESC, id DESC
LIMIT ?`,
		projectID,
		formatTime(since.UTC()),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		entry, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) init(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}

	pragmas := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
	}
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	schema := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slug TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			fingerprint TEXT NOT NULL,
			fingerprint_material TEXT NOT NULL DEFAULT '',
			fingerprint_explanation_json TEXT NOT NULL DEFAULT '[]',
			title TEXT NOT NULL,
			normalized_title TEXT NOT NULL,
			exception_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'unresolved',
			resolved_at TEXT NOT NULL DEFAULT '',
			reopened_at TEXT NOT NULL DEFAULT '',
			last_regressed_at TEXT NOT NULL DEFAULT '',
			regression_count INTEGER NOT NULL DEFAULT 0,
			first_seen TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			event_count INTEGER NOT NULL,
			representative_event_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(project_id, fingerprint)
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
			fingerprint TEXT NOT NULL,
			fingerprint_material TEXT NOT NULL DEFAULT '',
			fingerprint_explanation_json TEXT NOT NULL DEFAULT '[]',
			received_at TEXT NOT NULL,
			observed_at TEXT NOT NULL,
			severity TEXT NOT NULL,
			message TEXT NOT NULL,
			regressed INTEGER NOT NULL DEFAULT 0,
			event_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS event_facets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
			issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
			section TEXT NOT NULL,
			facet_key TEXT NOT NULL,
			facet_value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS releases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			environment TEXT NOT NULL DEFAULT '',
			observed_at TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '',
			commit_sha TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			severity TEXT NOT NULL DEFAULT '',
			rule_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(project_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS source_maps (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			release TEXT NOT NULL,
			dist TEXT NOT NULL DEFAULT '',
			bundle_url TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			content_type TEXT NOT NULL DEFAULT '',
			source_map_blob BLOB NOT NULL DEFAULT X'',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_bcrypt TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			project_id INTEGER NOT NULL REFERENCES projects(id),
			key_sha256 TEXT UNIQUE NOT NULL,
			created_at TEXT NOT NULL,
			last_used_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_project_last_seen ON issues(project_id, last_seen DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_events_issue_id ON events(project_id, issue_id, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_events_project_received_at ON events(project_id, received_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_releases_project_observed_at ON releases(project_id, observed_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_event_facets_lookup ON event_facets(project_id, section, facet_key, facet_value)`,
	}
	for _, stmt := range schema {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if err := ensureColumn(ctx, tx, "issues", "fingerprint_material", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "fingerprint_explanation_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "status", "TEXT NOT NULL DEFAULT 'unresolved'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "resolved_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "reopened_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "last_regressed_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "regression_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "events", "fingerprint_material", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "events", "fingerprint_explanation_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "events", "regressed", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "source_maps", "source_map_blob", "BLOB NOT NULL DEFAULT X''"); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO projects (slug, name)
VALUES (?, ?)
ON CONFLICT(slug) DO NOTHING`,
		defaultProject,
		"Default Project",
	); err != nil {
		return err
	}

	var projectID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, defaultProject).Scan(&projectID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	s.defaultProjectID = projectID
	return nil
}

func (s *Store) upsertIssue(ctx context.Context, projectID int64, processed worker.ProcessedEvent) (Issue, int64, bool, error) {
	evt := processed.Event
	fingerprintValue := strings.TrimSpace(processed.Fingerprint)
	if fingerprintValue == "" {
		fingerprintValue = fingerprintFromEvent(evt)
	}
	material := strings.TrimSpace(processed.FingerprintMaterial)
	if material == "" {
		material = evt.FingerprintMaterial
	}
	explanation := processed.FingerprintExplanation
	if len(explanation) == 0 {
		explanation = evt.FingerprintExplanation
	}

	title, normalizedTitle, exceptionType := issueDetails(evt)
	seenAt := issueSeenAt(evt)
	representative, err := marshalEvent(evt)
	if err != nil {
		return Issue{}, 0, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Issue{}, 0, false, err
	}
	defer tx.Rollback()

	var (
		id                   int64
		issue                Issue
		existing             bool
		eventCount           int
		firstSeen            string
		lastSeen             string
		resolvedAt           string
		reopenedAt           string
		lastRegressedAt      string
		regressionCount      int
		status               string
		storedMaterial       string
		storedExplanation    []byte
		storedRepresentative []byte
	)

	err = tx.QueryRowContext(ctx, `
SELECT
	id,
	fingerprint,
	fingerprint_material,
	fingerprint_explanation_json,
	title,
	normalized_title,
	exception_type,
	status,
	resolved_at,
	reopened_at,
	last_regressed_at,
	regression_count,
	first_seen,
	last_seen,
	event_count,
	representative_event_json
FROM issues
WHERE project_id = ? AND fingerprint = ?`,
		projectID,
		fingerprintValue,
	).Scan(
		&id,
		&issue.Fingerprint,
		&storedMaterial,
		&storedExplanation,
		&issue.Title,
		&issue.NormalizedTitle,
		&issue.ExceptionType,
		&status,
		&resolvedAt,
		&reopenedAt,
		&lastRegressedAt,
		&regressionCount,
		&firstSeen,
		&lastSeen,
		&eventCount,
		&storedRepresentative,
	)
	switch {
	case err == nil:
		existing = true
	case errors.Is(err, sql.ErrNoRows):
		existing = false
	case err != nil:
		return Issue{}, 0, false, err
	}

	if existing {
		parsedFirstSeen, err := parseTime(firstSeen)
		if err != nil {
			return Issue{}, 0, false, err
		}
		parsedLastSeen, err := parseTime(lastSeen)
		if err != nil {
			return Issue{}, 0, false, err
		}
		issue.ID = formatID(issueIDPrefix, id)
		issue.FirstSeen = parsedFirstSeen
		issue.LastSeen = parsedLastSeen
		issue.EventCount = eventCount + 1
		issue.Status = status
		issue.RegressionCount = regressionCount
		issue.FingerprintMaterial = storedMaterial
		issue.ResolvedAt, _ = parseTime(resolvedAt)
		issue.ReopenedAt, _ = parseTime(reopenedAt)
		issue.LastRegressedAt, _ = parseTime(lastRegressedAt)
		if err := unmarshalStringSlice(storedExplanation, &issue.FingerprintExplanation); err != nil {
			return Issue{}, 0, false, err
		}
		if err := json.Unmarshal(storedRepresentative, &issue.RepresentativeEvent); err != nil {
			return Issue{}, 0, false, err
		}
		if issue.FingerprintMaterial == "" {
			issue.FingerprintMaterial = material
		}
		if len(issue.FingerprintExplanation) == 0 {
			issue.FingerprintExplanation = explanation
		}
		if seenAt.After(issue.LastSeen) {
			issue.LastSeen = seenAt
		}

		regressed := issue.Status == "resolved"
		if regressed {
			issue.Status = "unresolved"
			issue.RegressionCount++
			issue.ReopenedAt = seenAt
			issue.LastRegressedAt = seenAt
		}

		assignments := []string{
			"last_seen = ?",
			"event_count = event_count + 1",
			"updated_at = CURRENT_TIMESTAMP",
		}
		args := []any{formatTime(issue.LastSeen)}
		if regressed {
			assignments = append(assignments, "status = 'unresolved'", "reopened_at = ?", "last_regressed_at = ?", "regression_count = regression_count + 1")
			args = append(args, formatTime(issue.ReopenedAt), formatTime(issue.LastRegressedAt))
		}
		args = append(args, id, projectID)

		if _, err := tx.ExecContext(ctx, `
UPDATE issues
SET `+strings.Join(assignments, ", ")+`
WHERE id = ? AND project_id = ?`,
			args...,
		); err != nil {
			return Issue{}, 0, false, err
		}

		if err := tx.Commit(); err != nil {
			return Issue{}, 0, false, err
		}
		issue.Fingerprint = fingerprintValue
		issue.RepresentativeEvent = evt
		return issue, id, regressed, nil
	}

	issue = Issue{
		Fingerprint:            fingerprintValue,
		FingerprintMaterial:    material,
		FingerprintExplanation: explanation,
		Title:                  title,
		NormalizedTitle:        normalizedTitle,
		ExceptionType:          exceptionType,
		Status:                 "unresolved",
		FirstSeen:              seenAt,
		LastSeen:               seenAt,
		EventCount:             1,
		RepresentativeEvent:    evt,
	}

	res, err := tx.ExecContext(ctx, `
INSERT INTO issues (
	project_id,
	fingerprint,
	fingerprint_material,
	fingerprint_explanation_json,
	title,
	normalized_title,
	exception_type,
	status,
	first_seen,
	last_seen,
	event_count,
	representative_event_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		fingerprintValue,
		material,
		mustMarshalStrings(explanation),
		title,
		normalizedTitle,
		exceptionType,
		"unresolved",
		formatTime(seenAt),
		formatTime(seenAt),
		1,
		representative,
	)
	if err != nil {
		return Issue{}, 0, false, err
	}

	id, err = res.LastInsertId()
	if err != nil {
		return Issue{}, 0, false, err
	}
	issue.ID = formatID(issueIDPrefix, id)

	if err := tx.Commit(); err != nil {
		return Issue{}, 0, false, err
	}

	return issue, id, false, nil
}

func (s *Store) insertEvent(ctx context.Context, projectID int64, issueID int64, regressed bool, processed worker.ProcessedEvent) (Event, int64, error) {
	payload, err := marshalEvent(processed.Event)
	if err != nil {
		return Event{}, 0, err
	}

	receivedAt, observedAt := eventTimestamps(processed.Event)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
INSERT INTO events (
	project_id,
	issue_id,
	fingerprint,
	fingerprint_material,
	fingerprint_explanation_json,
	received_at,
	observed_at,
	severity,
	message,
	regressed,
	event_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		issueID,
		processed.Fingerprint,
		processed.FingerprintMaterial,
		mustMarshalStrings(processed.FingerprintExplanation),
		formatTime(receivedAt),
		formatTime(observedAt),
		processed.Event.Severity,
		processed.Event.Message,
		boolToInt(regressed),
		payload,
	)
	if err != nil {
		return Event{}, 0, err
	}

	eventRowID, err := res.LastInsertId()
	if err != nil {
		return Event{}, 0, err
	}

	if err := tx.Commit(); err != nil {
		return Event{}, 0, err
	}

	return Event{
		ID:                     formatID(eventIDPrefix, eventRowID),
		IssueID:                formatID(issueIDPrefix, issueID),
		Fingerprint:            processed.Fingerprint,
		FingerprintMaterial:    processed.FingerprintMaterial,
		FingerprintExplanation: processed.FingerprintExplanation,
		ReceivedAt:             receivedAt,
		ObservedAt:             observedAt,
		Severity:               processed.Event.Severity,
		Message:                processed.Event.Message,
		Regressed:              regressed,
		Payload:                processed.Event,
	}, eventRowID, nil
}

func (s *Store) insertFacets(ctx context.Context, projectID int64, issueID, eventID int64, processed event.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	facets := map[string]any{
		"severity":   processed.Severity,
		"message":    processed.Message,
		"traceId":    processed.TraceID,
		"spanId":     processed.SpanID,
		"sdkName":    processed.SDKName,
		"exception":  processed.Exception,
		"attributes": processed.Attributes,
		"resource":   processed.Resource,
	}

	rows := make([]facetRow, 0, 32)
	flattenFacets(&rows, "", facets)
	for _, row := range rows {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO event_facets (project_id, event_id, issue_id, section, facet_key, facet_value)
VALUES (?, ?, ?, ?, ?, ?)`,
			projectID,
			eventID,
			issueID,
			row.section,
			row.key,
			row.value,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

type facetRow struct {
	section string
	key     string
	value   string
}

func flattenFacets(out *[]facetRow, prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenFacets(out, next, nested)
		}
	case []any:
		for idx, nested := range typed {
			next := fmt.Sprintf("%s[%d]", prefix, idx)
			flattenFacets(out, next, nested)
		}
	case string:
		if strings.TrimSpace(typed) == "" || strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{section: rootSection(prefix), key: prefix, value: typed})
	case fmt.Stringer:
		value := typed.String()
		if strings.TrimSpace(value) == "" || strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{section: rootSection(prefix), key: prefix, value: value})
	case bool:
		if strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{section: rootSection(prefix), key: prefix, value: strconv.FormatBool(typed)})
	case float64:
		if strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{section: rootSection(prefix), key: prefix, value: strconv.FormatFloat(typed, 'f', -1, 64)})
	case int:
		if strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{section: rootSection(prefix), key: prefix, value: strconv.Itoa(typed)})
	case int64:
		if strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{section: rootSection(prefix), key: prefix, value: strconv.FormatInt(typed, 10)})
	case json.Number:
		if strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{section: rootSection(prefix), key: prefix, value: typed.String()})
	case nil:
		return
	default:
		if strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{section: rootSection(prefix), key: prefix, value: fmt.Sprint(typed)})
	}
}

func rootSection(key string) string {
	if idx := strings.IndexByte(key, '.'); idx >= 0 {
		return key[:idx]
	}
	if idx := strings.IndexByte(key, '['); idx >= 0 {
		return key[:idx]
	}
	return key
}

func scanIssue(scanner interface {
	Scan(dest ...any) error
}) (Issue, error) {
	var (
		id                int64
		issue             Issue
		representativeRaw []byte
		explanationRaw    []byte
		firstSeen         string
		lastSeen          string
		resolvedAt        string
		reopenedAt        string
		lastRegressedAt   string
	)
	if err := scanner.Scan(
		&id,
		&issue.Fingerprint,
		&issue.FingerprintMaterial,
		&explanationRaw,
		&issue.Title,
		&issue.NormalizedTitle,
		&issue.ExceptionType,
		&issue.Status,
		&resolvedAt,
		&reopenedAt,
		&lastRegressedAt,
		&issue.RegressionCount,
		&firstSeen,
		&lastSeen,
		&issue.EventCount,
		&representativeRaw,
	); err != nil {
		return Issue{}, err
	}
	if err := unmarshalStringSlice(explanationRaw, &issue.FingerprintExplanation); err != nil {
		return Issue{}, err
	}
	if err := json.Unmarshal(representativeRaw, &issue.RepresentativeEvent); err != nil {
		return Issue{}, err
	}
	parsedFirstSeen, err := parseTime(firstSeen)
	if err != nil {
		return Issue{}, err
	}
	parsedLastSeen, err := parseTime(lastSeen)
	if err != nil {
		return Issue{}, err
	}
	issue.FirstSeen = parsedFirstSeen
	issue.LastSeen = parsedLastSeen
	issue.ResolvedAt, _ = parseTime(resolvedAt)
	issue.ReopenedAt, _ = parseTime(reopenedAt)
	issue.LastRegressedAt, _ = parseTime(lastRegressedAt)
	issue.ID = formatID(issueIDPrefix, id)
	return issue, nil
}

func scanEvent(scanner interface {
	Scan(dest ...any) error
}) (Event, error) {
	var (
		id             int64
		issueID        int64
		entry          Event
		payload        []byte
		explanationRaw []byte
		receivedAt     string
		observedAt     string
		regressed      int
	)
	if err := scanner.Scan(
		&id,
		&issueID,
		&entry.Fingerprint,
		&entry.FingerprintMaterial,
		&explanationRaw,
		&receivedAt,
		&observedAt,
		&entry.Severity,
		&entry.Message,
		&regressed,
		&payload,
	); err != nil {
		return Event{}, err
	}
	if err := unmarshalStringSlice(explanationRaw, &entry.FingerprintExplanation); err != nil {
		return Event{}, err
	}
	if err := json.Unmarshal(payload, &entry.Payload); err != nil {
		return Event{}, err
	}
	parsedReceivedAt, err := parseTime(receivedAt)
	if err != nil {
		return Event{}, err
	}
	parsedObservedAt, err := parseTime(observedAt)
	if err != nil {
		return Event{}, err
	}
	entry.ReceivedAt = parsedReceivedAt
	entry.ObservedAt = parsedObservedAt
	entry.Regressed = regressed != 0
	entry.ID = formatID(eventIDPrefix, id)
	entry.IssueID = formatID(issueIDPrefix, issueID)
	return entry, nil
}

func issueSeenAt(evt event.Event) time.Time {
	if !evt.ObservedAt.IsZero() {
		return evt.ObservedAt.UTC()
	}
	if !evt.ReceivedAt.IsZero() {
		return evt.ReceivedAt.UTC()
	}
	return time.Now().UTC()
}

func eventTimestamps(evt event.Event) (time.Time, time.Time) {
	receivedAt := evt.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = issueSeenAt(evt)
	}
	observedAt := evt.ObservedAt
	if observedAt.IsZero() {
		observedAt = receivedAt
	}
	return receivedAt.UTC(), observedAt.UTC()
}

func formatTime(value time.Time) string {
	return value.UTC().Format(timeLayout)
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeLayout, value)
}

func formatID(prefix string, value int64) string {
	return fmt.Sprintf("%s%06d", prefix, value)
}

func parseID(prefix, value string) (int64, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, prefix) {
		return 0, fmt.Errorf("invalid id %q", value)
	}
	return strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
}

func sqliteDSN(path string) string {
	u := url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}
	return u.String() + "?cache=shared&mode=rwc&_busy_timeout=5000"
}

func marshalEvent(evt event.Event) ([]byte, error) {
	return json.Marshal(evt)
}

func fingerprintFromEvent(evt event.Event) string {
	exceptionType := strings.TrimSpace(evt.Exception.Type)
	message := strings.TrimSpace(evt.Exception.Message)
	if message == "" {
		message = strings.TrimSpace(evt.Message)
	}

	var title string
	switch {
	case exceptionType != "" && message != "":
		title = exceptionType + ": " + message
	case exceptionType != "":
		title = exceptionType
	default:
		title = message
	}

	return normalizeTitle(title)
}

func issueDetails(evt event.Event) (title, normalizedTitle, exceptionType string) {
	exceptionType = strings.TrimSpace(evt.Exception.Type)
	message := strings.TrimSpace(evt.Exception.Message)
	if message == "" {
		message = strings.TrimSpace(evt.Message)
	}

	switch {
	case exceptionType != "" && message != "":
		title = exceptionType + ": " + message
	case exceptionType != "":
		title = exceptionType
	default:
		title = message
	}

	normalizedTitle = normalizeTitle(title)
	return title, normalizedTitle, exceptionType
}

func normalizeTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = uuidPattern.ReplaceAllString(value, "<id>")
	value = ipv4Pattern.ReplaceAllString(value, "<ip>")
	value = hexAddress.ReplaceAllString(value, "<hex>")
	value = longNumber.ReplaceAllString(value, "<num>")
	value = pathNumber.ReplaceAllString(value, "/:num")
	value = whitespace.ReplaceAllString(value, " ")
	value = trimPunctuation.ReplaceAllString(value, "")
	return value
}

func ensureColumn(ctx context.Context, tx *sql.Tx, table, column, definition string) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	return err
}

func mustMarshalStrings(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func unmarshalStringSlice(raw []byte, dest *[]string) error {
	if len(raw) == 0 {
		*dest = nil
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		*dest = nil
		return err
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// User represents an admin user stored in the database.
type User struct {
	ID             int64
	Username       string
	PasswordBcrypt string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Project represents a project row.
type Project struct {
	ID        int64
	Name      string
	Slug      string
	CreatedAt time.Time
}

// APIKey represents an API key row (the plaintext key is never stored).
type APIKey struct {
	ID         int64
	Name       string
	ProjectID  int64
	KeySHA256  string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// UpsertUser creates a user or updates their password if the username already exists.
func (s *Store) UpsertUser(ctx context.Context, username, passwordBcrypt string) error {
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO users (username, password_bcrypt, created_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(username) DO UPDATE SET password_bcrypt = excluded.password_bcrypt, updated_at = excluded.updated_at`,
		username, passwordBcrypt, now, now,
	)
	return err
}

// CreateProject inserts a new project row; returns an error if the slug already exists.
func (s *Store) CreateProject(ctx context.Context, name, slug string) (Project, error) {
	now := formatTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO projects (name, slug, created_at) VALUES (?, ?, ?)`,
		name, slug, now,
	)
	if err != nil {
		return Project{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Project{}, err
	}
	return Project{ID: id, Name: name, Slug: slug, CreatedAt: time.Now().UTC()}, nil
}

// ListProjects returns all projects ordered by id.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, slug, created_at FROM projects ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &createdAt); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = parseTime(createdAt)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// CreateAPIKey stores an API key's SHA-256 hash and returns the resulting row.
func (s *Store) CreateAPIKey(ctx context.Context, name string, projectID int64, keySHA256 string) (APIKey, error) {
	now := formatTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO api_keys (name, project_id, key_sha256, created_at) VALUES (?, ?, ?, ?)`,
		name, projectID, keySHA256, now,
	)
	if err != nil {
		return APIKey{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return APIKey{}, err
	}
	return APIKey{ID: id, Name: name, ProjectID: projectID, KeySHA256: keySHA256, CreatedAt: time.Now().UTC()}, nil
}

// ListAPIKeys returns all API key rows (without the plaintext key).
func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, project_id, key_sha256, created_at, last_used_at
FROM api_keys ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var createdAt string
		var lastUsedAt sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.ProjectID, &k.KeySHA256, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		k.CreatedAt, _ = parseTime(createdAt)
		if lastUsedAt.Valid {
			k.LastUsedAt, _ = parseTime(lastUsedAt.String)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// DeleteAPIKey removes the API key with the given id.
func (s *Store) DeleteAPIKey(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TouchAPIKey updates last_used_at for the key matching the given SHA-256 hex.
func (s *Store) TouchAPIKey(ctx context.Context, keySHA256 string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE api_keys SET last_used_at = ? WHERE key_sha256 = ?`,
		formatTime(time.Now().UTC()), keySHA256,
	)
	return err
}

// ValidAPIKeySHA256 returns the project_id for the API key matching the given SHA-256 hex digest.
// Returns (0, false, nil) when no matching key exists.
func (s *Store) ValidAPIKeySHA256(ctx context.Context, keySHA256 string) (projectID int64, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT project_id FROM api_keys WHERE key_sha256 = ?`, keySHA256).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return projectID, true, nil
}

// EnsureProject returns the project with the given slug, creating it if it does not exist.
func (s *Store) EnsureProject(ctx context.Context, slug string) (Project, error) {
	p, err := s.ProjectBySlug(ctx, slug)
	if err == nil {
		return p, nil
	}
	return s.CreateProject(ctx, slug, slug)
}

// ProjectBySlug returns the project with the given slug.
func (s *Store) ProjectBySlug(ctx context.Context, slug string) (Project, error) {
	var p Project
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, slug, created_at FROM projects WHERE slug = ?`, slug).
		Scan(&p.ID, &p.Name, &p.Slug, &createdAt)
	if err != nil {
		return Project{}, err
	}
	p.CreatedAt, _ = parseTime(createdAt)
	return p, nil
}
