package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

// InsertPageView writes a single raw page-view event.
func (s *AnalyticsStore) InsertPageView(ctx context.Context, pv analytics.PageView) error {
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
func (s *AnalyticsStore) RollupDailyAnalytics(ctx context.Context, projectID int64, date time.Time) error {
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
func (s *AnalyticsStore) DeleteOldPageviews(ctx context.Context, cutoff time.Time) error {
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
