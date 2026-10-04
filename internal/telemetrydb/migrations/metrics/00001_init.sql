-- +goose Up
-- Per-minute host samples (cpu.util, load1, mem.avail_pct, fs.<mount>.used_pct,
-- net.<if>.rx_bps, ...). Kept 7 days; the size cap evicts the oldest by ts.
CREATE TABLE samples_1m (
    host   TEXT    NOT NULL,
    metric TEXT    NOT NULL,
    ts     INTEGER NOT NULL, -- unix ms, minute-aligned
    value  REAL    NOT NULL,
    PRIMARY KEY (host, metric, ts)
) WITHOUT ROWID;
CREATE INDEX idx_samples_1m_ts ON samples_1m (ts);

-- Hourly rollups of samples_1m, kept 90 days.
CREATE TABLE samples_1h (
    host   TEXT    NOT NULL,
    metric TEXT    NOT NULL,
    ts     INTEGER NOT NULL, -- unix ms, hour-aligned
    min    REAL    NOT NULL,
    avg    REAL    NOT NULL,
    max    REAL    NOT NULL,
    n      INTEGER NOT NULL,
    PRIMARY KEY (host, metric, ts)
) WITHOUT ROWID;
CREATE INDEX idx_samples_1h_ts ON samples_1h (ts);

-- Every host that ever reported, for the host list and the heartbeat rule.
CREATE TABLE hosts (
    host      TEXT    PRIMARY KEY,
    cores     INTEGER NOT NULL DEFAULT 0,
    last_seen INTEGER NOT NULL -- unix ms
) WITHOUT ROWID;

-- +goose Down
DROP TABLE hosts;
DROP TABLE samples_1h;
DROP TABLE samples_1m;
