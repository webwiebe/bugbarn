package storage

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// HourlyEventCounts returns 24-hour event counts per issue for the given issue row IDs.
// The returned map keys are issue row IDs. Index 0 of [24]int is the oldest hour, index 23
// is the most recent partial hour.
func (s *IssueStore) HourlyEventCounts(ctx context.Context, issueIDs []int64) (map[int64][24]int, error) {
	if len(issueIDs) == 0 {
		return map[int64][24]int{}, nil
	}

	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	query, args := hourlyCountsQuery(projectID, issueIDs)
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
		bucketIdx, inWindow, err := hourBucketIndex(now, hourBucket)
		if err != nil {
			slog.WarnContext(ctx, "storage: malformed hour_bucket timestamp; skipping",
				"issue_id", issueID, "hour_bucket", hourBucket, "error", err)
			continue
		}
		if !inWindow {
			continue
		}
		counts := result[issueID]
		counts[bucketIdx] += cnt
		result[issueID] = counts
	}
	return result, rows.Err()
}

// hourlyCountsQuery builds the per-issue, per-hour count query over the last
// 24 hours, filtered to projectID when it is not 0.
func hourlyCountsQuery(projectID int64, issueIDs []int64) (string, []any) {
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
	SUM(sample_weight) AS cnt
FROM events
WHERE ` + projectFilter + `issue_id IN (` + strings.Join(placeholders, ",") + `)
  AND observed_at >= datetime('now', '-24 hours')
GROUP BY issue_id, hour_bucket`
	return query, args
}

// hourBucketIndex maps an hour bucket ("2006-01-02T15", UTC) to its index in
// the 24-hour series: 0 is 23 hours ago, 23 is the current hour. inWindow is
// false for a bucket 24 or more hours old, or an hour or more in the future.
// Hours ago truncates toward zero, so a bucket less than an hour ahead of now
// counts as the current hour.
func hourBucketIndex(now time.Time, bucket string) (idx int, inWindow bool, err error) {
	t, err := time.Parse("2006-01-02T15", bucket)
	if err != nil {
		return 0, false, err
	}
	hoursAgo := int(now.Sub(t.UTC()).Hours())
	if hoursAgo < 0 || hoursAgo >= 24 {
		return 0, false, nil
	}
	return 23 - hoursAgo, true, nil
}
