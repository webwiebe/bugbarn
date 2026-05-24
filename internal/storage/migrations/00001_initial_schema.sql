-- +goose Up

CREATE TABLE IF NOT EXISTS project_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS projects (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    slug          TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'active',
    issue_prefix  TEXT NOT NULL DEFAULT '',
    issue_counter INTEGER NOT NULL DEFAULT 0,
    group_id      INTEGER REFERENCES project_groups(id) ON DELETE SET NULL,
    created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    username        TEXT UNIQUE NOT NULL,
    password_bcrypt TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS issues (
    id                           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id                   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fingerprint                  TEXT NOT NULL,
    fingerprint_material         TEXT NOT NULL DEFAULT '',
    fingerprint_explanation_json TEXT NOT NULL DEFAULT '[]',
    title                        TEXT NOT NULL,
    normalized_title             TEXT NOT NULL,
    exception_type               TEXT NOT NULL,
    status                       TEXT NOT NULL DEFAULT 'unresolved',
    resolved_at                  TEXT NOT NULL DEFAULT '',
    reopened_at                  TEXT NOT NULL DEFAULT '',
    last_regressed_at            TEXT NOT NULL DEFAULT '',
    regression_count             INTEGER NOT NULL DEFAULT 0,
    mute_mode                    TEXT NOT NULL DEFAULT '',
    issue_number                 INTEGER NOT NULL DEFAULT 0,
    first_seen                   TEXT NOT NULL,
    last_seen                    TEXT NOT NULL,
    event_count                  INTEGER NOT NULL,
    representative_event_json    TEXT NOT NULL,
    created_at                   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(project_id, fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_issues_project_last_seen ON issues(project_id, last_seen DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_issues_project_status_last_seen ON issues(project_id, status, last_seen DESC, id DESC);

CREATE TABLE IF NOT EXISTS events (
    id                           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id                   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    issue_id                     INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    fingerprint                  TEXT NOT NULL,
    fingerprint_material         TEXT NOT NULL DEFAULT '',
    fingerprint_explanation_json TEXT NOT NULL DEFAULT '[]',
    received_at                  TEXT NOT NULL,
    observed_at                  TEXT NOT NULL,
    severity                     TEXT NOT NULL,
    message                      TEXT NOT NULL,
    regressed                    INTEGER NOT NULL DEFAULT 0,
    user_json                    TEXT NOT NULL DEFAULT '',
    breadcrumbs_json             TEXT NOT NULL DEFAULT '',
    event_json                   TEXT NOT NULL,
    created_at                   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_events_issue_id ON events(project_id, issue_id, id ASC);
CREATE INDEX IF NOT EXISTS idx_events_issue_observed ON events(project_id, issue_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_project_received_at ON events(project_id, received_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS event_facets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    event_id    INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    issue_id    INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    section     TEXT NOT NULL,
    facet_key   TEXT NOT NULL,
    facet_value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_event_facets_lookup ON event_facets(project_id, section, facet_key, facet_value);
CREATE INDEX IF NOT EXISTS idx_event_facets_issue ON event_facets(project_id, issue_id, facet_key, facet_value);

CREATE TABLE IF NOT EXISTS releases (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL,
    version     TEXT NOT NULL DEFAULT '',
    commit_sha  TEXT NOT NULL DEFAULT '',
    url         TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_releases_project_observed_at ON releases(project_id, observed_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS alerts (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    enabled          INTEGER NOT NULL DEFAULT 1,
    severity         TEXT NOT NULL DEFAULT '',
    rule_json        TEXT NOT NULL DEFAULT '{}',
    webhook_url      TEXT NOT NULL DEFAULT '',
    condition        TEXT NOT NULL DEFAULT 'new_issue',
    threshold        INTEGER NOT NULL DEFAULT 0,
    cooldown_minutes INTEGER NOT NULL DEFAULT 15,
    last_fired_at    TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(project_id, key)
);

CREATE TABLE IF NOT EXISTS source_maps (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    release         TEXT NOT NULL,
    dist            TEXT NOT NULL DEFAULT '',
    bundle_url      TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    content_type    TEXT NOT NULL DEFAULT '',
    source_map_blob BLOB NOT NULL DEFAULT X'',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    project_id   INTEGER NOT NULL REFERENCES projects(id),
    key_sha256   TEXT UNIQUE NOT NULL,
    scope        TEXT NOT NULL DEFAULT 'full',
    created_at   TEXT NOT NULL,
    last_used_at TEXT
);

CREATE TABLE IF NOT EXISTS alert_firings (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    alert_id INTEGER NOT NULL,
    issue_id INTEGER NOT NULL,
    fired_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_alert_firings_lookup ON alert_firings(alert_id, issue_id, fired_at DESC);

CREATE TABLE IF NOT EXISTS log_entries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL,
    received_at TEXT NOT NULL,
    level_num   INTEGER NOT NULL DEFAULT 30,
    level       TEXT NOT NULL DEFAULT 'info',
    message     TEXT NOT NULL,
    data_json   TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_log_entries_project_id ON log_entries(project_id, id DESC);

CREATE TABLE IF NOT EXISTS project_aliases (
    alias_slug TEXT PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS analytics_pageviews (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id        INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    ts                INTEGER NOT NULL,
    pathname          TEXT    NOT NULL DEFAULT '',
    hostname          TEXT    NOT NULL DEFAULT '',
    referrer_host     TEXT    NOT NULL DEFAULT '',
    referrer_path     TEXT    NOT NULL DEFAULT '',
    session_id        TEXT    NOT NULL DEFAULT '',
    visitor_id        TEXT    NOT NULL DEFAULT '',
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    screen_width      INTEGER NOT NULL DEFAULT 0,
    max_scroll_pct    INTEGER NOT NULL DEFAULT 0,
    interaction_count INTEGER NOT NULL DEFAULT 0,
    exit_pathname     TEXT    NOT NULL DEFAULT '',
    props             TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_analytics_pv_project_ts       ON analytics_pageviews(project_id, ts);
CREATE INDEX IF NOT EXISTS idx_analytics_pv_project_pathname ON analytics_pageviews(project_id, pathname, ts);
CREATE INDEX IF NOT EXISTS idx_analytics_pv_session          ON analytics_pageviews(project_id, session_id, ts);

CREATE TABLE IF NOT EXISTS analytics_daily (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    date       TEXT    NOT NULL,
    pathname   TEXT    NOT NULL DEFAULT '',
    dim_key    TEXT    NOT NULL DEFAULT '',
    dim_value  TEXT    NOT NULL DEFAULT '',
    pageviews  INTEGER NOT NULL DEFAULT 0,
    sessions   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, date, pathname, dim_key, dim_value)
);

CREATE INDEX IF NOT EXISTS idx_analytics_daily_project_date ON analytics_daily(project_id, date);

CREATE TABLE IF NOT EXISTS regression_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    issue_id     INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    regressed_at TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_regression_events_project_time ON regression_events(project_id, regressed_at DESC);

-- +goose Down

DROP TABLE IF EXISTS regression_events;
DROP TABLE IF EXISTS analytics_daily;
DROP TABLE IF EXISTS analytics_pageviews;
DROP TABLE IF EXISTS project_aliases;
DROP TABLE IF EXISTS log_entries;
DROP TABLE IF EXISTS alert_firings;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS source_maps;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS releases;
DROP TABLE IF EXISTS event_facets;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS issues;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS project_groups;
