package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func (s *Store) ListIssueEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]Event, bool, error) {
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
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

	const eventCols = `e.id, e.issue_id, e.fingerprint, e.fingerprint_material, e.fingerprint_explanation_json,
       e.received_at, e.observed_at, e.severity, e.message, e.regressed, e.event_json,
       i.issue_number, COALESCE(p.issue_prefix, '')`
	const eventJoin = ` FROM events e
JOIN issues i ON i.id = e.issue_id
JOIN projects p ON p.id = i.project_id`

	var rows *sql.Rows
	if projectID != 0 {
		if beforeID > 0 {
			rows, err = s.readDB().QueryContext(ctx, `SELECT `+eventCols+eventJoin+`
WHERE e.project_id = ? AND e.issue_id = ? AND e.id < ?
ORDER BY e.id DESC LIMIT ?`,
				projectID, rowID, beforeID, fetch)
		} else {
			rows, err = s.readDB().QueryContext(ctx, `SELECT `+eventCols+eventJoin+`
WHERE e.project_id = ? AND e.issue_id = ?
ORDER BY e.id DESC LIMIT ?`,
				projectID, rowID, fetch)
		}
	} else {
		if beforeID > 0 {
			rows, err = s.readDB().QueryContext(ctx, `SELECT `+eventCols+eventJoin+`
WHERE e.issue_id = ? AND e.id < ?
ORDER BY e.id DESC LIMIT ?`,
				rowID, beforeID, fetch)
		} else {
			rows, err = s.readDB().QueryContext(ctx, `SELECT `+eventCols+eventJoin+`
WHERE e.issue_id = ?
ORDER BY e.id DESC LIMIT ?`,
				rowID, fetch)
		}
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

	const sel = `
SELECT
	e.id,
	e.issue_id,
	e.fingerprint,
	e.fingerprint_material,
	e.fingerprint_explanation_json,
	e.received_at,
	e.observed_at,
	e.severity,
	e.message,
	e.regressed,
	e.event_json,
	i.issue_number,
	COALESCE(p.issue_prefix, '')
FROM events e
JOIN issues i ON i.id = e.issue_id
JOIN projects p ON p.id = i.project_id`

	var row *sql.Row
	if projectID != 0 {
		row = s.readDB().QueryRowContext(ctx, sel+`
WHERE e.project_id = ? AND e.id = ?`, projectID, rowID)
	} else {
		row = s.readDB().QueryRowContext(ctx, sel+`
WHERE e.id = ?`, rowID)
	}

	evt, err := scanEvent(row)
	if err != nil {
		return Event{}, wrapNotFound(err, "event not found")
	}
	return evt, nil
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

	var (
		rows *sql.Rows
		err  error
	)
	const recentSel = `
SELECT
	e.id,
	e.issue_id,
	e.fingerprint,
	e.fingerprint_material,
	e.fingerprint_explanation_json,
	e.received_at,
	e.observed_at,
	e.severity,
	e.message,
	e.regressed,
	e.event_json,
	i.issue_number,
	COALESCE(p.issue_prefix, '')
FROM events e
JOIN issues i ON i.id = e.issue_id
JOIN projects p ON p.id = i.project_id`
	sinceStr := formatTime(since.UTC())
	if projectID != 0 {
		rows, err = s.readDB().QueryContext(ctx, recentSel+`
WHERE e.project_id = ? AND e.received_at >= ?
ORDER BY e.received_at DESC, e.id DESC
LIMIT ?`, projectID, sinceStr, limit)
	} else {
		rows, err = s.readDB().QueryContext(ctx, recentSel+`
WHERE e.received_at >= ?
ORDER BY e.received_at DESC, e.id DESC
LIMIT ?`, sinceStr, limit)
	}
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

func (s *Store) insertEvent(ctx context.Context, projectID int64, issueID int64, issueDisplayID string, regressed bool, processed worker.ProcessedEvent) (Event, int64, error) {
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
		IssueID:                issueDisplayID,
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
		issueNumber    int
		issuePrefix    string
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
		&issueNumber,
		&issuePrefix,
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
	if issuePrefix != "" && issueNumber > 0 {
		entry.IssueID = formatIssueID(issuePrefix, issueNumber)
	} else {
		entry.IssueID = formatID(issueIDPrefix, issueID)
	}
	return entry, nil
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
