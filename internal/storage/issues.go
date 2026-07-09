package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

func (s *IssueStore) ListIssues(ctx context.Context) ([]Issue, error) {
	return s.ListIssuesFiltered(ctx, IssueFilter{})
}

func (s *IssueStore) ListIssuesFiltered(ctx context.Context, filter IssueFilter) (_ []Issue, retErr error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.ListIssuesFiltered")
	defer func() {
		if retErr != nil {
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()
	orderBy := issueOrderBy(filter)

	facetFilters := collectFacetFilters(filter)

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}
	allProjects := projectID == 0

	groupIDs, hasGroupFilter := ProjectIDsFromContext(ctx)

	conditions, whereArgs := issueWhereConditions(filter, projectID, allProjects, groupIDs, hasGroupFilter)

	// fromArgs holds bindings for subquery ?-placeholders in the FROM clause.
	// They must come before whereArgs in the final args slice.
	fromClause, fromArgs := buildIssueFromClause(facetFilters, projectID)

	// Combine: subquery bindings first (appear in FROM clause), then WHERE bindings.
	args := append(fromArgs, whereArgs...)

	sqlQuery := buildIssueListQuery(fromClause, conditions, orderBy, filter)

	rows, err := s.readDB().QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, wrapErr(err, "list issues")
	}
	defer rows.Close()

	return scanIssueRows(rows)
}

// buildIssueListQuery assembles the SELECT/FROM/WHERE/ORDER BY/LIMIT SQL for the
// issue list query from the already-built FROM clause, WHERE conditions, and
// ORDER BY expression.
func buildIssueListQuery(fromClause string, conditions []string, orderBy string, filter IssueFilter) string {
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
	return sqlQuery
}

// scanIssueRows materializes all rows from an issue list query into a slice.
func scanIssueRows(rows *sql.Rows) ([]Issue, error) {
	issues := []Issue{}
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, wrapErr(err, "scan issue")
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr(err, "list issues")
	}
	return issues, nil
}

// facetFilter is a single non-empty facet key/value constraint.
type facetFilter struct{ k, v string }

// collectFacetFilters returns the filter's non-empty facet key/value pairs.
func collectFacetFilters(filter IssueFilter) []facetFilter {
	var facetFilters []facetFilter
	for fk, fv := range filter.Facets {
		if strings.TrimSpace(fk) != "" && strings.TrimSpace(fv) != "" {
			facetFilters = append(facetFilters, facetFilter{fk, fv})
		}
	}
	return facetFilters
}

// buildIssueFromClause builds the FROM clause (and its subquery bind args) for
// the issue list query. When facet filters are present it joins an INTERSECT
// subquery that enforces AND semantics across all filters.
func buildIssueFromClause(facetFilters []facetFilter, projectID int64) (string, []any) {
	if len(facetFilters) == 0 {
		return "issues i LEFT JOIN projects p ON p.id = i.project_id", nil
	}
	// Build an INTERSECT subquery: one branch per facet filter that returns
	// matching issue_ids. The join enforces AND semantics across all filters.
	// Always project-scoped — facet filtering across all projects is intentionally
	// unsupported (no backing index, no caller needs it). With projectID==0 the
	// subquery matches nothing rather than full-scanning every project's facets.
	var fromArgs []any
	var subqueries []string
	for _, f := range facetFilters {
		subqueries = append(subqueries,
			`SELECT DISTINCT issue_id FROM event_facets WHERE project_id = ? AND facet_key = ? AND facet_value = ?`)
		fromArgs = append(fromArgs, projectID, f.k, f.v)
	}
	fromClause := fmt.Sprintf(`issues i INNER JOIN (%s) ef ON i.id = ef.issue_id LEFT JOIN projects p ON p.id = i.project_id`,
		strings.Join(subqueries, " INTERSECT "))
	return fromClause, fromArgs
}

// issueOrderBy maps the filter's sort/status to the ORDER BY clause.
func issueOrderBy(filter IssueFilter) string {
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
	return orderBy
}

// issueWhereConditions builds the WHERE conditions and their bound args from the
// project/group scope and the filter's status/query.
func issueWhereConditions(
	filter IssueFilter, projectID int64, allProjects bool, groupIDs []int64, hasGroupFilter bool,
) (conditions []string, whereArgs []any) {
	if hasGroupFilter {
		placeholders := make([]string, len(groupIDs))
		for i, id := range groupIDs {
			placeholders[i] = "?"
			whereArgs = append(whereArgs, id)
		}
		conditions = append(conditions, "i.project_id IN ("+strings.Join(placeholders, ",")+")")
	} else if !allProjects {
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

	return conditions, whereArgs
}

func (s *IssueStore) GetIssue(ctx context.Context, issueID string) (Issue, error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.GetIssue")
	defer span.End()
	span.SetAttributes(attribute.String("issue_id", issueID))
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return Issue{}, err
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
LEFT JOIN projects p ON p.id = i.project_id
WHERE i.id = ?`

	row := s.readDB().QueryRowContext(ctx, sel, rowID)
	issue, err := scanIssue(row)
	if err != nil {
		return Issue{}, wrapNotFound(err, "issue not found")
	}
	return issue, nil
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
	issue.ID = displayIssueID(issuePrefix, issueNumber, id)
	return issue, nil
}
