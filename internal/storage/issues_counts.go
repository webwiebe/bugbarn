package storage

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// HourlyEventCounts returns 24-hour event counts per issue for the given issue row IDs.
// The returned map keys are issue row IDs. Index 0 of [24]int is the oldest hour, index 23
// is the most recent partial hour.
func (s *IssueStore) HourlyEventCounts(ctx context.Context, issueIDs []int64) (map[int64][24]int, error) {
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
			slog.WarnContext(ctx, "storage: malformed hour_bucket timestamp; skipping",
				"issue_id", issueID, "hour_bucket", hourBucket, "error", err)
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
