package storage

import (
	"context"
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

//nolint:gocognit,gocyclo,funlen // dynamic filter/sort/facet query builder; tracked for refactor.
func (s *IssueStore) ListIssuesFiltered(ctx context.Context, filter IssueFilter) (_ []Issue, retErr error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.ListIssuesFiltered")
	defer func() {
		if retErr != nil {
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()
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

	groupIDs, hasGroupFilter := ProjectIDsFromContext(ctx)

	var conditions []string
	var whereArgs []any
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

	issues := []Issue{}
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
