package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
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
		orderBy = "i.first_seen DESC, i.id DESC"
	case "event_count":
		orderBy = "i.event_count DESC, i.id DESC"
	default:
		orderBy = "i.last_seen DESC, i.id DESC"
	}
	if filter.Status == "open" {
		orderBy = "(CASE WHEN i.status = 'regressed' THEN 0 ELSE 1 END), " + orderBy
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
	allProjects := projectID == 0

	var conditions []string
	var whereArgs []any
	if !allProjects {
		conditions = append(conditions, "i.project_id = ?")
		whereArgs = append(whereArgs, projectID)
	}

	switch filter.Status {
	case "open":
		conditions = append(conditions, "i.status IN ('unresolved', 'regressed')")
	case "muted":
		conditions = append(conditions, "i.status = 'muted'")
	case "resolved":
		conditions = append(conditions, "i.status = 'resolved'")
	// "all" or "" → no status filter
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
			if !allProjects {
				subqueries = append(subqueries,
					`SELECT DISTINCT issue_id FROM event_facets WHERE project_id = ? AND facet_key = ? AND facet_value = ?`)
				fromArgs = append(fromArgs, projectID, f.k, f.v)
			} else {
				subqueries = append(subqueries,
					`SELECT DISTINCT issue_id FROM event_facets WHERE facet_key = ? AND facet_value = ?`)
				fromArgs = append(fromArgs, f.k, f.v)
			}
		}
		fromClause = fmt.Sprintf(`issues i INNER JOIN (%s) ef ON i.id = ef.issue_id LEFT JOIN projects p ON p.id = i.project_id`,
			strings.Join(subqueries, " INTERSECT "))
	} else {
		fromClause = "issues i LEFT JOIN projects p ON p.id = i.project_id"
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
	i.mute_mode,
	i.resolved_at,
	i.reopened_at,
	i.last_regressed_at,
	i.regression_count,
	i.first_seen,
	i.last_seen,
	i.event_count,
	i.representative_event_json,
	COALESCE(p.slug, '') AS project_slug,
	i.issue_number,
	COALESCE(p.issue_prefix, '') AS issue_prefix
FROM ` + fromClause
	if len(conditions) > 0 {
		sqlQuery += `
WHERE ` + strings.Join(conditions, " AND ")
	}
	sqlQuery += `
ORDER BY ` + orderBy

	if filter.Limit > 0 {
		sqlQuery += fmt.Sprintf(" LIMIT %d", filter.Limit)
		if filter.Offset > 0 {
			sqlQuery += fmt.Sprintf(" OFFSET %d", filter.Offset)
		}
	}

	rows, err := s.readDB().QueryContext(ctx, sqlQuery, args...)
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
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	const sel = `
SELECT
	i.id,
	i.fingerprint,
	i.fingerprint_material,
	i.fingerprint_explanation_json,
	i.title,
	i.normalized_title,
	i.exception_type,
	i.status,
	i.mute_mode,
	i.resolved_at,
	i.reopened_at,
	i.last_regressed_at,
	i.regression_count,
	i.first_seen,
	i.last_seen,
	i.event_count,
	i.representative_event_json,
	COALESCE(p.slug, '') AS project_slug,
	i.issue_number,
	COALESCE(p.issue_prefix, '') AS issue_prefix
FROM issues i
LEFT JOIN projects p ON p.id = i.project_id`

	var row *sql.Row
	if projectID != 0 {
		row = s.readDB().QueryRowContext(ctx, sel+`
WHERE i.project_id = ? AND i.id = ?`, projectID, rowID)
	} else {
		row = s.readDB().QueryRowContext(ctx, sel+`
WHERE i.id = ?`, rowID)
	}

	issue, err := scanIssue(row)
	if err != nil {
		return Issue{}, wrapNotFound(err, "issue not found")
	}
	return issue, nil
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
		muteMode             string
		storedMaterial       string
		storedExplanation    []byte
		storedRepresentative []byte
	)

	var existingIssueNumber int
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
	mute_mode,
	resolved_at,
	reopened_at,
	last_regressed_at,
	regression_count,
	first_seen,
	last_seen,
	event_count,
	representative_event_json,
	issue_number
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
		&muteMode,
		&resolvedAt,
		&reopenedAt,
		&lastRegressedAt,
		&regressionCount,
		&firstSeen,
		&lastSeen,
		&eventCount,
		&storedRepresentative,
		&existingIssueNumber,
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
		issue.IssueNumber = existingIssueNumber
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

		issue.MuteMode = muteMode
		regressed := issue.Status == "resolved"

		if existing && muteMode == "until_regression" && (issue.Status == "resolved" || issue.Status == "muted") {
			// A new event on a muted-until-regression issue triggers a regression.
			regressed = true
		}

		if regressed {
			if muteMode == "forever" {
				// Keep muted; don't change status.
				regressed = false
			} else {
				newStatus := "unresolved"
				if muteMode == "until_regression" {
					newStatus = "regressed"
					issue.MuteMode = "" // unmute now that regression occurred
				}
				issue.Status = newStatus
				issue.RegressionCount++
				issue.ReopenedAt = seenAt
				issue.LastRegressedAt = seenAt
			}
		}

		assignments := []string{
			"last_seen = ?",
			"event_count = event_count + 1",
			"updated_at = CURRENT_TIMESTAMP",
		}
		args := []any{formatTime(issue.LastSeen)}
		if regressed {
			newStatus := issue.Status
			assignments = append(assignments,
				"status = ?",
				"mute_mode = ?",
				"reopened_at = ?",
				"last_regressed_at = ?",
				"regression_count = regression_count + 1",
			)
			args = append(args, newStatus, issue.MuteMode, formatTime(issue.ReopenedAt), formatTime(issue.LastRegressedAt))
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
		// Set display ID for existing issue.
		var existingPrefix string
		_ = s.readDB().QueryRowContext(ctx, `SELECT issue_prefix FROM projects WHERE id = ?`, projectID).Scan(&existingPrefix)
		if existingPrefix != "" && existingIssueNumber > 0 {
			issue.ID = formatIssueID(existingPrefix, existingIssueNumber)
		} else {
			issue.ID = formatID(issueIDPrefix, id)
		}
		return issue, id, regressed, nil
	}

	// Atomically increment the project's issue counter.
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET issue_counter = issue_counter + 1 WHERE id = ?`, projectID); err != nil {
		return Issue{}, 0, false, err
	}
	var issueNumber int
	var issuePrefix string
	if err := tx.QueryRowContext(ctx,
		`SELECT issue_counter, issue_prefix FROM projects WHERE id = ?`, projectID).Scan(&issueNumber, &issuePrefix); err != nil {
		return Issue{}, 0, false, err
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
		IssueNumber:            issueNumber,
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
	representative_event_json,
	issue_number
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		issueNumber,
	)
	if err != nil {
		return Issue{}, 0, false, err
	}

	id, err = res.LastInsertId()
	if err != nil {
		return Issue{}, 0, false, err
	}
	if issuePrefix != "" {
		issue.ID = formatIssueID(issuePrefix, issueNumber)
	} else {
		issue.ID = formatID(issueIDPrefix, id)
	}

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
		issueNumber       int
		issuePrefix       string
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
		&issue.MuteMode,
		&resolvedAt,
		&reopenedAt,
		&lastRegressedAt,
		&issue.RegressionCount,
		&firstSeen,
		&lastSeen,
		&issue.EventCount,
		&representativeRaw,
		&issue.ProjectSlug,
		&issueNumber,
		&issuePrefix,
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
	issue.IssueNumber = issueNumber
	if issuePrefix != "" && issueNumber > 0 {
		issue.ID = formatIssueID(issuePrefix, issueNumber)
	} else {
		issue.ID = formatID(issueIDPrefix, id)
	}
	return issue, nil
}

// MuteIssue sets an issue to muted status with the given mute mode.
// muteMode must be one of "until_regression" or "forever".
func (s *Store) MuteIssue(ctx context.Context, issueID string, muteMode string) (Issue, error) {
	if muteMode != "until_regression" && muteMode != "forever" {
		return Issue{}, apperr.InvalidInput(fmt.Sprintf("invalid mute_mode %q: must be 'until_regression' or 'forever'", muteMode), nil)
	}
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
	}
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}
	if projectID != 0 {
		_, err = s.db.ExecContext(ctx, `
UPDATE issues SET status = 'muted', mute_mode = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND project_id = ?`,
			muteMode, rowID, projectID)
	} else {
		_, err = s.db.ExecContext(ctx, `
UPDATE issues SET status = 'muted', mute_mode = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			muteMode, rowID)
	}
	if err != nil {
		return Issue{}, err
	}
	return s.GetIssue(ctx, issueID)
}

// UnmuteIssue clears mute status and sets the issue back to unresolved.
func (s *Store) UnmuteIssue(ctx context.Context, issueID string) (Issue, error) {
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
	}
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}
	var err2 error
	if projectID != 0 {
		_, err2 = s.db.ExecContext(ctx, `
UPDATE issues SET status = 'unresolved', mute_mode = '', updated_at = CURRENT_TIMESTAMP WHERE id = ? AND project_id = ?`,
			rowID, projectID)
	} else {
		_, err2 = s.db.ExecContext(ctx, `
UPDATE issues SET status = 'unresolved', mute_mode = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			rowID)
	}
	if err2 != nil {
		return Issue{}, err2
	}
	return s.GetIssue(ctx, issueID)
}

// HourlyEventCounts returns 24-hour event counts per issue for the given issue row IDs.
// The returned map keys are issue row IDs. Index 0 of [24]int is the oldest hour, index 23
// is the most recent partial hour.
func (s *Store) HourlyEventCounts(ctx context.Context, issueIDs []int64) (map[int64][24]int, error) {
	if len(issueIDs) == 0 {
		return map[int64][24]int{}, nil
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	// Build placeholder list.
	placeholders := make([]string, len(issueIDs))
	var args []any
	if projectID != 0 {
		args = append(args, projectID)
	}
	for i, id := range issueIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	var projectFilter string
	if projectID != 0 {
		projectFilter = "project_id = ? AND "
	}
	query := `
SELECT
	issue_id,
	strftime('%Y-%m-%dT%H', observed_at) AS hour_bucket,
	COUNT(*) AS cnt
FROM events
WHERE ` + projectFilter + `issue_id IN (` + strings.Join(placeholders, ",") + `)
  AND observed_at >= datetime('now', '-24 hours')
GROUP BY issue_id, hour_bucket`

	rows, err := s.readDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][24]int, len(issueIDs))
	now := time.Now().UTC()

	for rows.Next() {
		var issueID int64
		var hourBucket string
		var cnt int
		if err := rows.Scan(&issueID, &hourBucket, &cnt); err != nil {
			return nil, err
		}
		// Parse the hour bucket to determine how many hours ago it is.
		t, err := time.Parse("2006-01-02T15", hourBucket)
		if err != nil {
			continue
		}
		t = t.UTC()
		hoursAgo := int(now.Sub(t).Hours())
		if hoursAgo < 0 || hoursAgo >= 24 {
			continue
		}
		// Index 0 = 23 hours ago, index 23 = current hour.
		bucketIdx := 23 - hoursAgo
		if bucketIdx < 0 {
			bucketIdx = 0
		}
		counts := result[issueID]
		counts[bucketIdx] += cnt
		result[issueID] = counts
	}
	return result, rows.Err()
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
