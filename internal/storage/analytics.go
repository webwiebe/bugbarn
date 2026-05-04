package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

// InsertPageView writes a single raw page-view event.
func (s *Store) InsertPageView(ctx context.Context, pv analytics.PageView) error {
	props := "{}"
	if len(pv.Props) > 0 {
		b, err := json.Marshal(pv.Props)
		if err == nil {
			props = string(b)
		}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO analytics_pageviews
			(project_id, ts, pathname, hostname, referrer_host, referrer_path,
			 session_id, duration_ms, screen_width, props,
			 visitor_id, max_scroll_pct, interaction_count, exit_pathname)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pv.ProjectID,
		pv.Ts.Unix(),
		pv.Pathname,
		pv.Hostname,
		pv.ReferrerHost,
		pv.ReferrerPath,
		pv.SessionID,
		pv.DurationMs,
		pv.ScreenWidth,
		props,
		pv.VisitorID,
		pv.MaxScrollPct,
		pv.InteractionCount,
		pv.ExitPathname,
	)
	return err
}

// RollupDailyAnalytics aggregates raw pageviews for the given UTC date into
// analytics_daily. Uses INSERT OR REPLACE so it is safe to call multiple times.
func (s *Store) RollupDailyAnalytics(ctx context.Context, projectID int64, date time.Time) error {
	dateStr := date.UTC().Format("2006-01-02")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Per-pathname totals (dim_key='', dim_value='')
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO analytics_daily
			(project_id, date, pathname, dim_key, dim_value, pageviews, sessions)
		SELECT
			? AS project_id,
			? AS date,
			pathname,
			'' AS dim_key,
			'' AS dim_value,
			COUNT(*)                        AS pageviews,
			COUNT(DISTINCT session_id)      AS sessions
		FROM analytics_pageviews
		WHERE project_id = ?
		  AND strftime('%Y-%m-%d', ts, 'unixepoch') = ?
		GROUP BY pathname`,
		projectID, dateStr, projectID, dateStr,
	); err != nil {
		return fmt.Errorf("rollup per-pathname: %w", err)
	}

	// All-pages total row (pathname='')
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO analytics_daily
			(project_id, date, pathname, dim_key, dim_value, pageviews, sessions)
		SELECT
			? AS project_id,
			? AS date,
			'' AS pathname,
			'' AS dim_key,
			'' AS dim_value,
			COUNT(*)                   AS pageviews,
			COUNT(DISTINCT session_id) AS sessions
		FROM analytics_pageviews
		WHERE project_id = ?
		  AND strftime('%Y-%m-%d', ts, 'unixepoch') = ?
		HAVING COUNT(*) > 0`,
		projectID, dateStr, projectID, dateStr,
	); err != nil {
		return fmt.Errorf("rollup total: %w", err)
	}

	// Per-referrer_host breakdown (dim_key='referrer_host')
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO analytics_daily
			(project_id, date, pathname, dim_key, dim_value, pageviews, sessions)
		SELECT
			? AS project_id,
			? AS date,
			'' AS pathname,
			'referrer_host' AS dim_key,
			referrer_host   AS dim_value,
			COUNT(*)                   AS pageviews,
			COUNT(DISTINCT session_id) AS sessions
		FROM analytics_pageviews
		WHERE project_id = ?
		  AND strftime('%Y-%m-%d', ts, 'unixepoch') = ?
		GROUP BY referrer_host`,
		projectID, dateStr, projectID, dateStr,
	); err != nil {
		return fmt.Errorf("rollup referrers: %w", err)
	}

	return tx.Commit()
}

// DeleteOldPageviews removes raw rows older than cutoff.
func (s *Store) DeleteOldPageviews(ctx context.Context, cutoff time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM analytics_pageviews WHERE ts < ?`,
		cutoff.Unix(),
	)
	return err
}

// queryLimit returns the effective limit (default 50, max 500).
func queryLimit(q analytics.Query) int {
	if q.Limit <= 0 {
		return 50
	}
	if q.Limit > 500 {
		return 500
	}
	return q.Limit
}

// QueryOverview returns aggregate stats. Uses analytics_daily for the bulk of
// the range; includes un-rolled-up rows from analytics_pageviews for today.
func (s *Store) QueryOverview(ctx context.Context, q analytics.Query) (analytics.OverviewResult, error) {
	startStr := q.Start.UTC().Format("2006-01-02")
	endStr := q.End.UTC().Format("2006-01-02")

	// From daily rollups (excludes today which may not be rolled up yet)
	row := s.readDB().QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(d.pageviews), 0)    AS pageviews,
			COALESCE(SUM(d.sessions), 0)     AS sessions,
			COUNT(DISTINCT CASE WHEN d.pathname != '' THEN d.pathname END) AS pages
		FROM analytics_daily d
		WHERE d.project_id = ?
		  AND d.date BETWEEN ? AND ?
		  AND d.pathname = ''
		  AND d.dim_key = ''`,
		q.ProjectID, startStr, endStr,
	)
	var res analytics.OverviewResult
	if err := row.Scan(&res.Pageviews, &res.Sessions, &res.PagesCount); err != nil {
		return res, err
	}

	// Add today's un-rolled-up raw rows
	today := time.Now().UTC().Format("2006-01-02")
	raw := s.readDB().QueryRowContext(ctx, `
		SELECT
			COALESCE(COUNT(*), 0)                AS pageviews,
			COALESCE(COUNT(DISTINCT session_id), 0) AS sessions,
			COALESCE(COUNT(DISTINCT pathname), 0)   AS pages,
			COALESCE(AVG(NULLIF(duration_ms, 0)), 0) AS avg_dur
		FROM analytics_pageviews
		WHERE project_id = ?
		  AND strftime('%Y-%m-%d', ts, 'unixepoch') = ?
		  AND strftime('%Y-%m-%d', ts, 'unixepoch') BETWEEN ? AND ?`,
		q.ProjectID, today, startStr, endStr,
	)
	var rawPV, rawSess, rawPages int64
	var rawAvgDur float64
	if err := raw.Scan(&rawPV, &rawSess, &rawPages, &rawAvgDur); err != nil {
		return res, err
	}
	res.Pageviews += rawPV
	res.Sessions += rawSess
	if rawPages > res.PagesCount {
		res.PagesCount = rawPages
	}
	res.AvgDurationMs = int64(rawAvgDur)
	return res, nil
}

// QueryPages returns per-pathname stats ordered by pageviews DESC.
func (s *Store) QueryPages(ctx context.Context, q analytics.Query) ([]analytics.PageStat, error) {
	startStr := q.Start.UTC().Format("2006-01-02")
	endStr := q.End.UTC().Format("2006-01-02")
	limit := queryLimit(q)

	rows, err := s.readDB().QueryContext(ctx, `
		SELECT pathname, SUM(pageviews) AS pv, SUM(sessions) AS sess
		FROM analytics_daily
		WHERE project_id = ?
		  AND date BETWEEN ? AND ?
		  AND pathname != ''
		  AND dim_key = ''
		GROUP BY pathname
		ORDER BY pv DESC
		LIMIT ?`,
		q.ProjectID, startStr, endStr, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []analytics.PageStat{}
	for rows.Next() {
		var p analytics.PageStat
		if err := rows.Scan(&p.Pathname, &p.Pageviews, &p.Sessions); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// QueryTimeline returns bucketed time series. granularity: "day", "week", "month".
func (s *Store) QueryTimeline(ctx context.Context, q analytics.Query, granularity string) ([]analytics.TimelineBucket, error) {
	startStr := q.Start.UTC().Format("2006-01-02")
	endStr := q.End.UTC().Format("2006-01-02")

	var groupExpr string
	switch granularity {
	case "week":
		groupExpr = `strftime('%Y-W%W', date)`
	case "month":
		groupExpr = `strftime('%Y-%m', date)`
	default:
		groupExpr = `date`
	}

	query := fmt.Sprintf(`
		SELECT %s AS bucket, SUM(pageviews), SUM(sessions)
		FROM analytics_daily
		WHERE project_id = ?
		  AND date BETWEEN ? AND ?
		  AND pathname = ''
		  AND dim_key = ''
		GROUP BY bucket
		ORDER BY bucket ASC`,
		groupExpr,
	)
	rows, err := s.readDB().QueryContext(ctx, query, q.ProjectID, startStr, endStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []analytics.TimelineBucket{}
	for rows.Next() {
		var b analytics.TimelineBucket
		if err := rows.Scan(&b.Date, &b.Pageviews, &b.Sessions); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// QueryReferrers returns top referring hostnames.
func (s *Store) QueryReferrers(ctx context.Context, q analytics.Query) ([]analytics.ReferrerStat, error) {
	startStr := q.Start.UTC().Format("2006-01-02")
	endStr := q.End.UTC().Format("2006-01-02")
	limit := queryLimit(q)

	rows, err := s.readDB().QueryContext(ctx, `
		SELECT dim_value, SUM(pageviews), SUM(sessions)
		FROM analytics_daily
		WHERE project_id = ?
		  AND date BETWEEN ? AND ?
		  AND dim_key = 'referrer_host'
		GROUP BY dim_value
		ORDER BY SUM(pageviews) DESC
		LIMIT ?`,
		q.ProjectID, startStr, endStr, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []analytics.ReferrerStat{}
	for rows.Next() {
		var r analytics.ReferrerStat
		if err := rows.Scan(&r.Host, &r.Pageviews, &r.Sessions); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QuerySegments returns a breakdown by a named props key using raw pageviews.
// We use the raw table because props are not pre-aggregated beyond referrer_host.
func (s *Store) QuerySegments(ctx context.Context, q analytics.Query, dimKey string) ([]analytics.SegmentBucket, error) {
	if strings.TrimSpace(dimKey) == "" {
		return []analytics.SegmentBucket{}, nil
	}
	limit := queryLimit(q)

	rows, err := s.readDB().QueryContext(ctx, `
		SELECT
			json_extract(props, '$.' || ?) AS val,
			COUNT(*)                        AS pageviews,
			COUNT(DISTINCT session_id)      AS sessions
		FROM analytics_pageviews
		WHERE project_id = ?
		  AND ts BETWEEN ? AND ?
		  AND json_extract(props, '$.' || ?) IS NOT NULL
		GROUP BY val
		ORDER BY pageviews DESC
		LIMIT ?`,
		dimKey,
		q.ProjectID,
		q.Start.Unix(), q.End.Unix(),
		dimKey,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []analytics.SegmentBucket{}
	for rows.Next() {
		var b analytics.SegmentBucket
		if err := rows.Scan(&b.Value, &b.Pageviews, &b.Sessions); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// QueryPageFlow returns page-flow data for a given pathname.
func (s *Store) QueryPageFlow(ctx context.Context, q analytics.Query, pathname string) (analytics.PageFlowResult, error) {
	result := analytics.PageFlowResult{
		Pathname: pathname,
		CameFrom: []analytics.FlowEntry{},
		WentTo:   []analytics.FlowEntry{},
	}

	// WentTo — aggregate exit_pathname from raw pageviews
	wentToRows, err := s.readDB().QueryContext(ctx, `
		SELECT exit_pathname, COUNT(*) as cnt, COUNT(DISTINCT session_id) as sess
		FROM analytics_pageviews
		WHERE project_id = ? AND pathname = ? AND exit_pathname != ''
		  AND ts BETWEEN ? AND ?
		GROUP BY exit_pathname ORDER BY cnt DESC LIMIT 5`,
		q.ProjectID, pathname, q.Start.Unix(), q.End.Unix(),
	)
	if err != nil {
		return result, err
	}
	defer wentToRows.Close()

	var wentToTotal int64
	for wentToRows.Next() {
		var e analytics.FlowEntry
		var sess int64
		if err := wentToRows.Scan(&e.Pathname, &e.Count, &sess); err != nil {
			return result, err
		}
		wentToTotal += e.Count
		result.WentTo = append(result.WentTo, e)
	}
	if err := wentToRows.Err(); err != nil {
		return result, err
	}
	for i := range result.WentTo {
		if wentToTotal > 0 {
			result.WentTo[i].Pct = float64(result.WentTo[i].Count) / float64(wentToTotal) * 100
		}
	}

	// CameFrom — find what page users were on just before visiting pathname in the same session
	cameFromRows, err := s.readDB().QueryContext(ctx, `
		SELECT prev.pathname, COUNT(*) as cnt
		FROM analytics_pageviews cur
		JOIN analytics_pageviews prev
		  ON prev.project_id = cur.project_id
		 AND prev.session_id = cur.session_id
		 AND prev.ts < cur.ts
		 AND prev.pathname != cur.pathname
		WHERE cur.project_id = ? AND cur.pathname = ?
		  AND cur.ts BETWEEN ? AND ?
		  AND prev.ts = (
		    SELECT MAX(p2.ts) FROM analytics_pageviews p2
		    WHERE p2.project_id = cur.project_id
		      AND p2.session_id = cur.session_id
		      AND p2.ts < cur.ts
		      AND p2.pathname != cur.pathname
		  )
		GROUP BY prev.pathname ORDER BY cnt DESC LIMIT 5`,
		q.ProjectID, pathname, q.Start.Unix(), q.End.Unix(),
	)
	if err != nil {
		return result, err
	}
	defer cameFromRows.Close()

	var cameFromTotal int64
	for cameFromRows.Next() {
		var e analytics.FlowEntry
		if err := cameFromRows.Scan(&e.Pathname, &e.Count); err != nil {
			return result, err
		}
		cameFromTotal += e.Count
		result.CameFrom = append(result.CameFrom, e)
	}
	if err := cameFromRows.Err(); err != nil {
		return result, err
	}
	for i := range result.CameFrom {
		if cameFromTotal > 0 {
			result.CameFrom[i].Pct = float64(result.CameFrom[i].Count) / float64(cameFromTotal) * 100
		}
	}

	return result, nil
}

// QueryScrollDepth returns scroll-depth bucket counts for a pathname.
func (s *Store) QueryScrollDepth(ctx context.Context, q analytics.Query, pathname string) (analytics.ScrollDepthResult, error) {
	result := analytics.ScrollDepthResult{
		Pathname: pathname,
		Buckets:  []analytics.ScrollBucket{},
	}

	rows, err := s.readDB().QueryContext(ctx, `
		SELECT
		  CASE
		    WHEN max_scroll_pct < 25  THEN '0–24%'
		    WHEN max_scroll_pct < 50  THEN '25–49%'
		    WHEN max_scroll_pct < 75  THEN '50–74%'
		    WHEN max_scroll_pct < 100 THEN '75–99%'
		    ELSE '100%'
		  END AS bucket,
		  COUNT(*) AS cnt
		FROM analytics_pageviews
		WHERE project_id = ? AND pathname = ? AND ts BETWEEN ? AND ?
		GROUP BY bucket ORDER BY MIN(max_scroll_pct)`,
		q.ProjectID, pathname, q.Start.Unix(), q.End.Unix(),
	)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	var total int64
	for rows.Next() {
		var b analytics.ScrollBucket
		if err := rows.Scan(&b.Label, &b.Count); err != nil {
			return result, err
		}
		total += b.Count
		result.Buckets = append(result.Buckets, b)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	for i := range result.Buckets {
		if total > 0 {
			result.Buckets[i].Pct = float64(result.Buckets[i].Count) / float64(total) * 100
		}
	}

	return result, nil
}

// QueryDropout returns pages ordered by bounce (zero-interaction) sessions.
func (s *Store) QueryDropout(ctx context.Context, q analytics.Query) ([]analytics.DropoutStat, error) {
	limit := queryLimit(q)

	rows, err := s.readDB().QueryContext(ctx, `
		SELECT
		  pathname,
		  COUNT(*) AS pageviews,
		  SUM(CASE WHEN interaction_count = 0 THEN 1 ELSE 0 END) AS bounced
		FROM analytics_pageviews
		WHERE project_id = ? AND ts BETWEEN ? AND ? AND pathname != ''
		GROUP BY pathname
		ORDER BY bounced DESC
		LIMIT ?`,
		q.ProjectID, q.Start.Unix(), q.End.Unix(), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []analytics.DropoutStat{}
	for rows.Next() {
		var s analytics.DropoutStat
		if err := rows.Scan(&s.Pathname, &s.Pageviews, &s.BouncedSessions); err != nil {
			return nil, err
		}
		if s.Pageviews > 0 {
			s.BounceRate = float64(s.BouncedSessions) / float64(s.Pageviews)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListProjectIDs returns all project IDs (used by the rollup worker).
func (s *Store) ListProjectIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT id FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
