package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

const issueEventCols = `e.id, e.issue_id, e.fingerprint, e.fingerprint_material, e.fingerprint_explanation_json,
       e.received_at, e.observed_at, e.severity, e.message, e.regressed, e.event_json,
       i.issue_number, COALESCE(p.issue_prefix, '')`

const issueEventJoin = ` FROM events e
JOIN issues i ON i.id = e.issue_id
JOIN projects p ON p.id = i.project_id`

// issueEventsWhere builds the WHERE clause and arguments of ListIssueEvents:
// the issue, the project when one is set, and events older than beforeID when
// it is positive.
func issueEventsWhere(projectID, issueRowID, beforeID int64) (string, []any) {
	var conds []string
	var args []any
	if projectID != 0 {
		conds = append(conds, "e.project_id = ?")
		args = append(args, projectID)
	}
	conds = append(conds, "e.issue_id = ?")
	args = append(args, issueRowID)
	if beforeID > 0 {
		conds = append(conds, "e.id < ?")
		args = append(args, beforeID)
	}
	return "\nWHERE " + strings.Join(conds, " AND "), args
}

func (s *EventStore) ListIssueEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]Event, bool, error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.ListIssueEvents")
	defer span.End()
	span.SetAttributes(attribute.String("issue_id", issueID), attribute.Int("limit", limit))
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return nil, false, err
	}

	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	if limit <= 0 {
		limit = 25
	}

	// Fetch one extra row to know whether there are more pages.
	fetch := limit + 1

	where, args := issueEventsWhere(projectID, rowID, beforeID)
	rows, err := s.readDB().QueryContext(ctx, `SELECT `+issueEventCols+issueEventJoin+where+`
ORDER BY e.id DESC LIMIT ?`, append(args, fetch)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	events := []Event{}
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

// eventSelect selects an event with its issue number and project prefix.
// Callers append the WHERE clause.
const eventSelect = `
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

func (s *EventStore) GetEvent(ctx context.Context, eventID string) (Event, error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.GetEvent")
	defer span.End()
	span.SetAttributes(attribute.String("event_id", eventID))
	rowID, err := parseID(eventIDPrefix, eventID)
	if err != nil {
		return Event{}, err
	}

	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	var row *sql.Row
	if projectID != 0 {
		row = s.readDB().QueryRowContext(ctx, eventSelect+`
WHERE e.project_id = ? AND e.id = ?`, projectID, rowID)
	} else {
		row = s.readDB().QueryRowContext(ctx, eventSelect+`
WHERE e.id = ?`, rowID)
	}

	evt, err := scanEvent(row)
	if err != nil {
		return Event{}, wrapNotFound(err, "event not found")
	}
	return evt, nil
}

// LastEventReceivedAt returns the receipt time of the most recently persisted
// event across all projects, or the zero time if no events exist. It is used by
// the ingest-health monitor to detect a stalled write pipeline (no event
// persisted for an unexpectedly long window) — the failure mode behind the
// 2026-06-21 outage, which was invisible because reads kept working.
func (s *EventStore) LastEventReceivedAt(ctx context.Context) (time.Time, error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.LastEventReceivedAt")
	defer span.End()

	var raw sql.NullString
	if err := s.readDB().QueryRowContext(ctx, `SELECT MAX(received_at) FROM events`).Scan(&raw); err != nil {
		return time.Time{}, wrapErr(err, "query last event time")
	}
	if !raw.Valid {
		return time.Time{}, nil
	}
	return parseTime(raw.String)
}

func (s *EventStore) ListRecentEvents(ctx context.Context, limit int, since time.Time) ([]Event, error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.ListRecentEvents")
	defer span.End()
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-15 * time.Minute)
	}

	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	var (
		rows *sql.Rows
		err  error
	)
	sinceStr := formatTime(since.UTC())
	if projectID != 0 {
		rows, err = s.readDB().QueryContext(ctx, eventSelect+`
WHERE e.project_id = ? AND e.received_at >= ?
ORDER BY e.received_at DESC, e.id DESC
LIMIT ?`, projectID, sinceStr, limit)
	} else {
		rows, err = s.readDB().QueryContext(ctx, eventSelect+`
WHERE e.received_at >= ?
ORDER BY e.received_at DESC, e.id DESC
LIMIT ?`, sinceStr, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []Event{}
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

// insertEventSQL stores one event row; the arguments follow the column order.
const insertEventSQL = `
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
	event_json,
	sample_weight
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (s *core) insertEvent(
	ctx context.Context, projectID int64, issueID int64, issueDisplayID string,
	regressed bool, sampleWeight int64, processed event.ProcessedEvent,
) (Event, error) {
	payload, err := marshalEvent(processed.Event)
	if err != nil {
		return Event{}, err
	}

	receivedAt, observedAt := eventTimestamps(processed.Event)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, insertEventSQL,
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
		sampleWeight,
	)
	if err != nil {
		return Event{}, err
	}

	eventRowID, err := res.LastInsertId()
	if err != nil {
		return Event{}, err
	}

	if err := tx.Commit(); err != nil {
		return Event{}, err
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
	}, nil
}

// insertFacets projects the event's whole flattened payload into the issue's
// facet set. It is issue-level and deduplicated by the primary key, so the
// second and every later event of an issue that carries the same values writes
// nothing. Keys the projection does not accept are skipped rather than stored.
func (s *core) insertFacets(ctx context.Context, projectID int64, issueID int64, processed event.Event) error {
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
		if facetProjectionExcluded(row.key, row.value) {
			continue
		}
		if _, err := tx.ExecContext(ctx, insertIssueFacetSQL, projectID, issueID, row.key, row.value); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// facetScalar renders a leaf value as facet text. Blank strings and blank
// Stringers, and nil, have no facet; every other scalar is rendered.
func facetScalar(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "", false
	case string:
		return typed, strings.TrimSpace(typed) != ""
	case fmt.Stringer:
		text := typed.String()
		return text, strings.TrimSpace(text) != ""
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	default:
		return fmt.Sprint(typed), true
	}
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
	default:
		text, ok := facetScalar(value)
		if !ok || strings.TrimSpace(prefix) == "" {
			return
		}
		*out = append(*out, facetRow{key: prefix, value: text})
	}
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
	entry.IssueID = displayIssueID(issuePrefix, issueNumber, issueID)
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
