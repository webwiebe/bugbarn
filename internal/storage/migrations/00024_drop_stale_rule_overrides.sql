-- +goose Up
-- Prod stores whole-rule overrides of disk-full and load-high from 2026-10-05.
-- Each one only added the "host ne MacBookPro.localdomain" match, but an override
-- replaces the built-in wholesale, so later changes to the built-ins never took
-- effect: the disk-full exclusions (INF-73) and the load5 rule (INF-43). That match
-- now lives in defaults.json, so the rows carry nothing the built-ins lack. Removing
-- exactly these two ids lets the built-ins take over; other overrides and custom
-- rules stay.
DELETE FROM detection_rules WHERE id IN ('disk-full', 'load-high');

-- +goose Down
-- The deleted rows held a copy of the old built-ins; there is nothing to restore.
SELECT 1;
