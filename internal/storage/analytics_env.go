package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

// envFilter returns the WHERE fragment and argument that restrict a raw
// page-view read to q.Environment. col is the column or alias-qualified props
// reference. An empty environment adds nothing.
func envFilter(q analytics.Query, col string) (string, []any) {
	if q.Environment == "" {
		return "", nil
	}
	return fmt.Sprintf(" AND json_extract(%s, '$.environment') = ?", col), []any{q.Environment}
}

// rawRange is the shared project and time predicate of the environment reads.
const rawRange = `project_id = ? AND ts BETWEEN ? AND ?`

func rawRangeArgs(q analytics.Query) []any {
	return []any{q.ProjectID, q.Start.Unix(), q.End.AddDate(0, 0, 1).Unix() - 1}
}

// queryOverviewEnv answers the overview from raw page views, because the daily
// rollups hold no environment.
func (s *AnalyticsStore) queryOverviewEnv(ctx context.Context, q analytics.Query) (analytics.OverviewResult, error) {
	f, fa := envFilter(q, "props")
	var res analytics.OverviewResult
	var avg float64
	err := s.readDB().QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT session_id), COUNT(DISTINCT pathname),
		       COALESCE(AVG(NULLIF(duration_ms, 0)), 0)
		FROM analytics_pageviews
		WHERE `+rawRange+f, append(rawRangeArgs(q), fa...)...,
	).Scan(&res.Pageviews, &res.Sessions, &res.PagesCount, &avg)
	if err != nil {
		return res, wrapErr(err, "query analytics")
	}
	res.AvgDurationMs = int64(avg)
	return res, nil
}

// queryPagesEnv returns per-pathname stats for one environment.
func (s *AnalyticsStore) queryPagesEnv(ctx context.Context, q analytics.Query) ([]analytics.PageStat, error) {
	f, fa := envFilter(q, "props")
	args := append(append(rawRangeArgs(q), fa...), queryLimit(q))
	return queryRows(ctx, s.readDB(), `
		SELECT pathname, COUNT(*), COUNT(DISTINCT session_id)
		FROM analytics_pageviews
		WHERE `+rawRange+` AND pathname != ''`+f+`
		GROUP BY pathname ORDER BY COUNT(*) DESC LIMIT ?`, args,
		func(rows *sql.Rows) (analytics.PageStat, error) {
			var p analytics.PageStat
			err := rows.Scan(&p.Pathname, &p.Pageviews, &p.Sessions)
			return p, err
		})
}

// queryReferrersEnv returns the top referring hosts for one environment.
func (s *AnalyticsStore) queryReferrersEnv(ctx context.Context, q analytics.Query) ([]analytics.ReferrerStat, error) {
	f, fa := envFilter(q, "props")
	args := append(append(rawRangeArgs(q), fa...), queryLimit(q))
	return queryRows(ctx, s.readDB(), `
		SELECT referrer_host, COUNT(*), COUNT(DISTINCT session_id)
		FROM analytics_pageviews
		WHERE `+rawRange+` AND referrer_host != ''`+f+`
		GROUP BY referrer_host ORDER BY COUNT(*) DESC LIMIT ?`, args,
		func(rows *sql.Rows) (analytics.ReferrerStat, error) {
			var r analytics.ReferrerStat
			err := rows.Scan(&r.Host, &r.Pageviews, &r.Sessions)
			return r, err
		})
}

// queryTimelineEnv buckets one environment's page views by day, week or month.
func (s *AnalyticsStore) queryTimelineEnv(
	ctx context.Context, q analytics.Query, granularity string,
) ([]analytics.TimelineBucket, error) {
	var bucket string
	switch granularity {
	case "week":
		bucket = `strftime('%Y-W%W', ts, 'unixepoch')`
	case "month":
		bucket = `strftime('%Y-%m', ts, 'unixepoch')`
	default:
		bucket = `strftime('%Y-%m-%d', ts, 'unixepoch')`
	}
	f, fa := envFilter(q, "props")
	return queryRows(ctx, s.readDB(), `
		SELECT `+bucket+` AS bucket, COUNT(*), COUNT(DISTINCT session_id)
		FROM analytics_pageviews
		WHERE `+rawRange+f+`
		GROUP BY bucket ORDER BY bucket ASC`, append(rawRangeArgs(q), fa...),
		func(rows *sql.Rows) (analytics.TimelineBucket, error) {
			var b analytics.TimelineBucket
			err := rows.Scan(&b.Date, &b.Pageviews, &b.Sessions)
			return b, err
		})
}
