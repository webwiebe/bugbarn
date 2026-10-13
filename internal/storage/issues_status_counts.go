package storage

import (
	"context"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// IssueStatusCounts counts issues per status for the project or group scope in
// ctx, with the same scope rules as ListIssuesFiltered. Regressed counts open
// issues that came back after a resolve or an until-regression mute; it stays
// set until the issue is resolved again. The grouped query is answered from
// idx_issues_project_status_regression without touching the table.
func (s *IssueStore) IssueStatusCounts(ctx context.Context) (domain.IssueCounts, error) {
	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}
	groupIDs, hasGroup := domain.ProjectIDsFromContext(ctx)
	conditions, args := issueWhereConditions(domain.IssueFilter{}, projectID, projectID == 0, groupIDs, hasGroup)

	query := "SELECT i.status, i.regression_count > 0, COUNT(*) FROM issues i"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " GROUP BY i.status, i.regression_count > 0"

	rows, err := s.readDB().QueryContext(ctx, query, args...)
	if err != nil {
		return domain.IssueCounts{}, wrapErr(err, "count issues by status")
	}
	defer rows.Close()

	var counts domain.IssueCounts
	for rows.Next() {
		var status string
		var regressed bool
		var n int
		if err := rows.Scan(&status, &regressed, &n); err != nil {
			return domain.IssueCounts{}, wrapErr(err, "scan issue status count")
		}
		counts.All += n
		switch status {
		case "unresolved", "regressed":
			counts.Open += n
			if regressed || status == "regressed" {
				counts.Regressed += n
			}
		case "resolved":
			counts.Resolved += n
		case "muted":
			counts.Muted += n
		}
	}
	return counts, wrapErr(rows.Err(), "count issues by status")
}
