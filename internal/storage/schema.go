package storage

import (
	"context"
	"database/sql"
	"fmt"
)

func (s *Store) init(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}

	pragmas := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
	}
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	schema := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slug TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			fingerprint TEXT NOT NULL,
			fingerprint_material TEXT NOT NULL DEFAULT '',
			fingerprint_explanation_json TEXT NOT NULL DEFAULT '[]',
			title TEXT NOT NULL,
			normalized_title TEXT NOT NULL,
			exception_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'unresolved',
			resolved_at TEXT NOT NULL DEFAULT '',
			reopened_at TEXT NOT NULL DEFAULT '',
			last_regressed_at TEXT NOT NULL DEFAULT '',
			regression_count INTEGER NOT NULL DEFAULT 0,
			first_seen TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			event_count INTEGER NOT NULL,
			representative_event_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(project_id, fingerprint)
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
			fingerprint TEXT NOT NULL,
			fingerprint_material TEXT NOT NULL DEFAULT '',
			fingerprint_explanation_json TEXT NOT NULL DEFAULT '[]',
			received_at TEXT NOT NULL,
			observed_at TEXT NOT NULL,
			severity TEXT NOT NULL,
			message TEXT NOT NULL,
			regressed INTEGER NOT NULL DEFAULT 0,
			event_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS event_facets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
			issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
			section TEXT NOT NULL,
			facet_key TEXT NOT NULL,
			facet_value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS releases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			environment TEXT NOT NULL DEFAULT '',
			observed_at TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '',
			commit_sha TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			severity TEXT NOT NULL DEFAULT '',
			rule_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(project_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS source_maps (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			release TEXT NOT NULL,
			dist TEXT NOT NULL DEFAULT '',
			bundle_url TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			content_type TEXT NOT NULL DEFAULT '',
			source_map_blob BLOB NOT NULL DEFAULT X'',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_bcrypt TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			project_id INTEGER NOT NULL REFERENCES projects(id),
			key_sha256 TEXT UNIQUE NOT NULL,
			scope TEXT NOT NULL DEFAULT 'full',
			created_at TEXT NOT NULL,
			last_used_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_project_last_seen ON issues(project_id, last_seen DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_events_issue_id ON events(project_id, issue_id, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_events_project_received_at ON events(project_id, received_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_releases_project_observed_at ON releases(project_id, observed_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_event_facets_lookup ON event_facets(project_id, section, facet_key, facet_value)`,
		`CREATE TABLE IF NOT EXISTS alert_firings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			alert_id INTEGER NOT NULL,
			issue_id INTEGER NOT NULL,
			fired_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_firings_lookup ON alert_firings(alert_id, issue_id, fired_at DESC)`,
		`CREATE TABLE IF NOT EXISTS log_entries (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id  INTEGER NOT NULL,
			received_at TEXT NOT NULL,
			level_num   INTEGER NOT NULL DEFAULT 30,
			level       TEXT NOT NULL DEFAULT 'info',
			message     TEXT NOT NULL,
			data_json   TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_log_entries_project_id ON log_entries(project_id, id DESC)`,
	}
	for _, stmt := range schema {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if err := ensureColumn(ctx, tx, "issues", "fingerprint_material", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "fingerprint_explanation_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "status", "TEXT NOT NULL DEFAULT 'unresolved'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "resolved_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "reopened_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "last_regressed_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "issues", "regression_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "events", "fingerprint_material", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "events", "fingerprint_explanation_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "events", "regressed", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "source_maps", "source_map_blob", "BLOB NOT NULL DEFAULT X''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "api_keys", "scope", "TEXT NOT NULL DEFAULT 'full'"); err != nil {
		return err
	}

	// Log entry columns (added in case an older DB exists without them)
	if err := ensureColumn(ctx, tx, "log_entries", "level_num", "INTEGER NOT NULL DEFAULT 30"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "log_entries", "data_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return err
	}

	// Alert delivery fields
	if err := ensureColumn(ctx, tx, "alerts", "webhook_url", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "alerts", "condition", "TEXT NOT NULL DEFAULT 'new_issue'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "alerts", "threshold", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "alerts", "cooldown_minutes", "INTEGER NOT NULL DEFAULT 15"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "alerts", "last_fired_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Issue mute
	if err := ensureColumn(ctx, tx, "issues", "mute_mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// User context and breadcrumbs on events
	if err := ensureColumn(ctx, tx, "events", "user_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "events", "breadcrumbs_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "projects", "status", "TEXT NOT NULL DEFAULT 'active'"); err != nil {
		return err
	}

	// Analytics tables
	analyticsSchema := []string{
		`CREATE TABLE IF NOT EXISTS analytics_pageviews (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			ts           INTEGER NOT NULL,
			pathname     TEXT    NOT NULL DEFAULT '',
			hostname     TEXT    NOT NULL DEFAULT '',
			referrer_host TEXT   NOT NULL DEFAULT '',
			referrer_path TEXT   NOT NULL DEFAULT '',
			session_id   TEXT    NOT NULL DEFAULT '',
			duration_ms  INTEGER NOT NULL DEFAULT 0,
			screen_width INTEGER NOT NULL DEFAULT 0,
			props        TEXT    NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_analytics_pv_project_ts       ON analytics_pageviews(project_id, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_analytics_pv_project_pathname  ON analytics_pageviews(project_id, pathname, ts)`,
		`CREATE TABLE IF NOT EXISTS analytics_daily (
			project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			date        TEXT    NOT NULL,
			pathname    TEXT    NOT NULL DEFAULT '',
			dim_key     TEXT    NOT NULL DEFAULT '',
			dim_value   TEXT    NOT NULL DEFAULT '',
			pageviews   INTEGER NOT NULL DEFAULT 0,
			sessions    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (project_id, date, pathname, dim_key, dim_value)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_analytics_daily_project_date ON analytics_daily(project_id, date)`,
	}
	for _, stmt := range analyticsSchema {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO projects (slug, name)
VALUES (?, ?)
ON CONFLICT(slug) DO NOTHING`,
		defaultProject,
		"Default Project",
	); err != nil {
		return err
	}

	var projectID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, defaultProject).Scan(&projectID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	s.defaultProjectID = projectID
	return nil
}

func ensureColumn(ctx context.Context, tx *sql.Tx, table, column, definition string) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	return err
}
