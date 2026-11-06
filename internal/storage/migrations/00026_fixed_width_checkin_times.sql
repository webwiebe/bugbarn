-- +goose Up
-- formatTime now writes a nine-digit fraction (#253). MarkMonitorMissed matches
-- last_checkin_at with "=" against formatTime of the check-in it saw, so a
-- monitor whose last check-in was stored in the old trimmed form
-- ("2026-11-06T01:00:55.12Z") would never match again and never be marked
-- missed. That is exactly a monitor that stopped checking in before the deploy.
-- This rewrites those values to the form formatTime writes now. Every other
-- timestamp column is compared with < or >, where an old value keeps working.
--
-- A stored value is UTC: 19 characters, then an optional fraction of 1 to 9
-- digits, then Z. Values that are empty or already 30 characters long stay.
-- The monitors table holds at most MaxMonitorsPerProject (100) rows per project.
UPDATE monitors
SET last_checkin_at = CASE
    WHEN length(last_checkin_at) = 20 THEN substr(last_checkin_at, 1, 19) || '.000000000Z'
    ELSE substr(last_checkin_at, 1, 20)
      || substr(substr(last_checkin_at, 21, length(last_checkin_at) - 21) || '000000000', 1, 9)
      || 'Z'
  END
WHERE last_checkin_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'
  AND (length(last_checkin_at) = 20
    OR (length(last_checkin_at) BETWEEN 22 AND 29 AND substr(last_checkin_at, 20, 1) = '.'));

-- +goose Down
-- The trimmed form cannot be restored from the fixed-width one without guessing
-- which zeros were written. An older binary parses fixed-width values, but its
-- MarkMonitorMissed formats the trimmed form and does not match one until that
-- monitor checks in again.
SELECT 1;
