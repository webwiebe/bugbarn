-- +goose Up
-- One row per normalized security log line (Traefik access, k8s audit,
-- sshd/journald, Cloudflare firewall events). Raw lines are best-effort
-- forensics: the size cap evicts the oldest rows by id. Keep the index count
-- low; every index is paid on each insert.
CREATE TABLE security_logs (
    id          INTEGER PRIMARY KEY,
    ts          INTEGER NOT NULL, -- event time, unix ms
    received_at INTEGER NOT NULL, -- unix ms
    source      TEXT    NOT NULL, -- traefik, k8saudit, sshd, journald, cloudflare
    host        TEXT    NOT NULL DEFAULT '',
    kind        TEXT    NOT NULL DEFAULT '',
    src_ip      TEXT    NOT NULL DEFAULT '',
    username    TEXT,
    action      TEXT    NOT NULL DEFAULT '',
    status      INTEGER NOT NULL DEFAULT 0,
    method      TEXT    NOT NULL DEFAULT '',
    path        TEXT    NOT NULL DEFAULT '',
    message     TEXT    NOT NULL DEFAULT '',
    raw         TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_security_logs_ts ON security_logs (ts);
CREATE INDEX idx_security_logs_ip_ts ON security_logs (src_ip, ts);
CREATE INDEX idx_security_logs_host_ts ON security_logs (host, ts);
CREATE INDEX idx_security_logs_source_kind_ts ON security_logs (source, kind, ts);
CREATE INDEX idx_security_logs_user_ts ON security_logs (username, ts) WHERE username IS NOT NULL;

-- Small key/value state, e.g. the Cloudflare poller's per-zone cursor.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;

-- +goose Down
DROP TABLE meta;
DROP TABLE security_logs;
