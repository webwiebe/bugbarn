package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

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

func issueSeenAt(evt event.Event) time.Time {
	if !evt.ObservedAt.IsZero() {
		return evt.ObservedAt.UTC()
	}
	if !evt.ReceivedAt.IsZero() {
		return evt.ReceivedAt.UTC()
	}
	return time.Now().UTC()
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
