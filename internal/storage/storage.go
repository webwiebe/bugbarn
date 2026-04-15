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

type Issue struct {
	ID                  string
	Fingerprint         string
	Title               string
	NormalizedTitle     string
	ExceptionType       string
	FirstSeen           time.Time
	LastSeen            time.Time
	EventCount          int
	RepresentativeEvent event.Event
}

type Event struct {
	ID          string
	IssueID     string
	Fingerprint string
	ReceivedAt  time.Time
	ObservedAt  time.Time
	Severity    string
	Message     string
	Payload     event.Event
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

func (s *Store) PersistProcessedEvent(ctx context.Context, processed worker.ProcessedEvent) (Issue, Event, error) {
	if s == nil || s.db == nil {
		return Issue{}, Event{}, errors.New("storage is nil")
	}

	issue, issueID, err := s.upsertIssue(ctx, processed.Event, processed.Fingerprint)
	if err != nil {
		return Issue{}, Event{}, err
	}

	eventRow, eventRowID, err := s.insertEvent(ctx, issueID, processed)
	if err != nil {
		return Issue{}, Event{}, err
	}

	if err := s.insertFacets(ctx, issueID, eventRowID, processed.Event); err != nil {
		return Issue{}, Event{}, err
	}

	return issue, eventRow, nil
}

func (s *Store) ListIssues(ctx context.Context) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
	id,
	fingerprint,
	title,
	normalized_title,
	exception_type,
	first_seen,
	last_seen,
	event_count,
	representative_event_json
FROM issues
WHERE project_id = ?
ORDER BY last_seen DESC, id DESC`,
		s.defaultProjectID,
	)
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

	row := s.db.QueryRowContext(ctx, `
SELECT
	id,
	fingerprint,
	title,
	normalized_title,
	exception_type,
	first_seen,
	last_seen,
	event_count,
	representative_event_json
FROM issues
WHERE project_id = ? AND id = ?`,
		s.defaultProjectID,
		rowID,
	)

	return scanIssue(row)
}

func (s *Store) ListIssueEvents(ctx context.Context, issueID string) ([]Event, error) {
	rowID, err := parseID(issueIDPrefix, issueID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
	id,
	issue_id,
	fingerprint,
	received_at,
	observed_at,
	severity,
	message,
	event_json
FROM events
WHERE project_id = ? AND issue_id = ?
ORDER BY id ASC`,
		s.defaultProjectID,
		rowID,
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

func (s *Store) GetEvent(ctx context.Context, eventID string) (Event, error) {
	rowID, err := parseID(eventIDPrefix, eventID)
	if err != nil {
		return Event{}, err
	}

	row := s.db.QueryRowContext(ctx, `
SELECT
	id,
	issue_id,
	fingerprint,
	received_at,
	observed_at,
	severity,
	message,
	event_json
FROM events
WHERE project_id = ? AND id = ?`,
		s.defaultProjectID,
		rowID,
	)

	return scanEvent(row)
}

func (s *Store) ListRecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
	id,
	issue_id,
	fingerprint,
	received_at,
	observed_at,
	severity,
	message,
	event_json
FROM events
WHERE project_id = ?
ORDER BY id DESC
LIMIT ?`,
		s.defaultProjectID,
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
			title TEXT NOT NULL,
			normalized_title TEXT NOT NULL,
			exception_type TEXT NOT NULL,
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
			received_at TEXT NOT NULL,
			observed_at TEXT NOT NULL,
			severity TEXT NOT NULL,
			message TEXT NOT NULL,
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
		`CREATE INDEX IF NOT EXISTS idx_issues_project_last_seen ON issues(project_id, last_seen DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_events_issue_id ON events(project_id, issue_id, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_event_facets_lookup ON event_facets(project_id, section, facet_key, facet_value)`,
	}
	for _, stmt := range schema {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
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

func (s *Store) upsertIssue(ctx context.Context, evt event.Event, providedFingerprint string) (Issue, int64, error) {
	fingerprint := strings.TrimSpace(providedFingerprint)
	if fingerprint == "" {
		fingerprint = fingerprintFromEvent(evt)
	}

	title, normalizedTitle, exceptionType := issueDetails(evt)
	seenAt := issueSeenAt(evt)
	representative, err := marshalEvent(evt)
	if err != nil {
		return Issue{}, 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Issue{}, 0, err
	}
	defer tx.Rollback()

	var (
		id         int64
		issue      Issue
		existing   bool
		eventCount int
		firstSeen  string
		lastSeen   string
	)

	err = tx.QueryRowContext(ctx, `
SELECT
	id,
	fingerprint,
	title,
	normalized_title,
	exception_type,
	first_seen,
	last_seen,
	event_count,
	representative_event_json
FROM issues
WHERE project_id = ? AND fingerprint = ?`,
		s.defaultProjectID,
		fingerprint,
	).Scan(
		&id,
		&issue.Fingerprint,
		&issue.Title,
		&issue.NormalizedTitle,
		&issue.ExceptionType,
		&firstSeen,
		&lastSeen,
		&eventCount,
		&representative,
	)
	switch {
	case err == nil:
		existing = true
	case errors.Is(err, sql.ErrNoRows):
		existing = false
	case err != nil:
		return Issue{}, 0, err
	}

	if existing {
		parsedFirstSeen, err := parseTime(firstSeen)
		if err != nil {
			return Issue{}, 0, err
		}
		parsedLastSeen, err := parseTime(lastSeen)
		if err != nil {
			return Issue{}, 0, err
		}

		issue.FirstSeen = parsedFirstSeen
		issue.LastSeen = parsedLastSeen
		issue.ID = formatID(issueIDPrefix, id)
		issue.EventCount = eventCount + 1
		if seenAt.After(issue.LastSeen) {
			issue.LastSeen = seenAt
		}

		if _, err := tx.ExecContext(ctx, `
UPDATE issues
SET last_seen = ?, event_count = event_count + 1, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND project_id = ?`,
			formatTime(issue.LastSeen),
			id,
			s.defaultProjectID,
		); err != nil {
			return Issue{}, 0, err
		}
	} else {
		issue = Issue{
			Fingerprint:         fingerprint,
			Title:               title,
			NormalizedTitle:     normalizedTitle,
			ExceptionType:       exceptionType,
			FirstSeen:           seenAt,
			LastSeen:            seenAt,
			EventCount:          1,
			RepresentativeEvent: evt,
		}

		res, err := tx.ExecContext(ctx, `
INSERT INTO issues (
	project_id,
	fingerprint,
	title,
	normalized_title,
	exception_type,
	first_seen,
	last_seen,
	event_count,
	representative_event_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.defaultProjectID,
			fingerprint,
			title,
			normalizedTitle,
			exceptionType,
			formatTime(seenAt),
			formatTime(seenAt),
			1,
			representative,
		)
		if err != nil {
			return Issue{}, 0, err
		}

		id, err = res.LastInsertId()
		if err != nil {
			return Issue{}, 0, err
		}
		issue.ID = formatID(issueIDPrefix, id)
	}

	if err := tx.Commit(); err != nil {
		return Issue{}, 0, err
	}

	issue.RepresentativeEvent = evt
	if existing {
		// Preserve the stored representative event for existing issues.
		if err := json.Unmarshal(representative, &issue.RepresentativeEvent); err != nil {
			return Issue{}, 0, err
		}
	}

	return issue, id, nil
}

func (s *Store) insertEvent(ctx context.Context, issueID int64, processed worker.ProcessedEvent) (Event, int64, error) {
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
	received_at,
	observed_at,
	severity,
	message,
	event_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.defaultProjectID,
		issueID,
		processed.Fingerprint,
		formatTime(receivedAt),
		formatTime(observedAt),
		processed.Event.Severity,
		processed.Event.Message,
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
		ID:          formatID(eventIDPrefix, eventRowID),
		IssueID:     formatID(issueIDPrefix, issueID),
		Fingerprint: processed.Fingerprint,
		ReceivedAt:  receivedAt,
		ObservedAt:  observedAt,
		Severity:    processed.Event.Severity,
		Message:     processed.Event.Message,
		Payload:     processed.Event,
	}, eventRowID, nil
}

func (s *Store) insertFacets(ctx context.Context, issueID, eventID int64, processed event.Event) error {
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
			s.defaultProjectID,
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
		firstSeen         string
		lastSeen          string
	)
	if err := scanner.Scan(
		&id,
		&issue.Fingerprint,
		&issue.Title,
		&issue.NormalizedTitle,
		&issue.ExceptionType,
		&firstSeen,
		&lastSeen,
		&issue.EventCount,
		&representativeRaw,
	); err != nil {
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
	issue.ID = formatID(issueIDPrefix, id)
	return issue, nil
}

func scanEvent(scanner interface {
	Scan(dest ...any) error
}) (Event, error) {
	var (
		id         int64
		issueID    int64
		entry      Event
		payload    []byte
		receivedAt string
		observedAt string
	)
	if err := scanner.Scan(
		&id,
		&issueID,
		&entry.Fingerprint,
		&receivedAt,
		&observedAt,
		&entry.Severity,
		&entry.Message,
		&payload,
	); err != nil {
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
	return u.String() + "?cache=shared&mode=rwc"
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
