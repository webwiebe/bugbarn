package ingesthealth

import (
	"context"
	"fmt"
	"time"
)

// Spool worker progress (#95).
//
// The writer's file-spool worker persists what lands in the writer's spool:
// every event when the readers proxy synchronously (staging), and release
// markers and direct writer ingest everywhere. The Redis write queue does not
// pass through it, so a stalled spool worker leaves the queue depth at 0 and
// the staleness rule reads the instance as idle. The worker's own progress is
// the signal: the bytes it has not processed yet, and when its cursor last
// moved.
//
// An empty spool is healthy however old the cursor is: an idle instance writes
// nothing. A backlog is a stall once the cursor has not moved for
// SpoolStallAfter, 30 minutes like the last-event rule's default. It has its
// own setting because StaleAfter is raised on idle instances (30 days on
// testing) where staleness cannot tell idle from stalled; a backlog can, so the
// spool check keeps 30 minutes there. A worker that drains slowly still
// rewrites its cursor after every record, so it never reaches the threshold. A
// writer restart during a rollout (the writer uses Recreate and its startup
// probe allows 10 minutes) holds the backlog for minutes, well under it.

// SpoolBacklog is one reading of the writer spool.
type SpoolBacklog struct {
	// Bytes the worker has not processed yet.
	Bytes int64
	// LastAdvanceAt is when the worker last moved its cursor; zero when it
	// never has.
	LastAdvanceAt time.Time
}

// sampleSpool reads the spool backlog into snap and works out how long the
// backlog has waited without progress.
func (m *Monitor) sampleSpool(ctx context.Context, snap *Snapshot, now time.Time) {
	if m.deps.SpoolBacklog == nil {
		return
	}
	b, err := m.deps.SpoolBacklog(ctx)
	if err != nil {
		m.logger.Error("ingest-health: read spool backlog", "error", err)
		return
	}
	snap.SpoolBacklogKnown = true
	snap.SpoolBacklogBytes = b.Bytes
	if !b.LastAdvanceAt.IsZero() {
		snap.SpoolLastAdvanceAt = b.LastAdvanceAt.UTC()
	}
	if m.spoolClearAt.IsZero() || b.Bytes <= 0 {
		m.spoolClearAt = now
	}
	if b.Bytes <= 0 {
		return
	}
	// The backlog cannot have waited longer than since the last sample that
	// found the spool empty: after days of idling the cursor is days old, and a
	// record appended a second ago must not count as days of stall. A monitor
	// that just started counts from its first sample for the same reason: a
	// reader pod replaced during a rollout has seen no empty sample yet, and
	// the writer may still be starting.
	since := b.LastAdvanceAt
	if m.spoolClearAt.After(since) {
		since = m.spoolClearAt
	}
	snap.SpoolStalledSeconds = max(now.Sub(since).Seconds(), 0)
}

// evaluateSpool flags a backlog whose cursor has not moved for SpoolStallAfter.
func (m *Monitor) evaluateSpool(snap *Snapshot) {
	if m.cfg.SpoolStallAfter <= 0 || !snap.SpoolBacklogKnown || snap.SpoolBacklogBytes <= 0 {
		return
	}
	stalled := time.Duration(snap.SpoolStalledSeconds * float64(time.Second))
	if stalled <= m.cfg.SpoolStallAfter {
		return
	}
	snap.Healthy = false
	snap.Reasons = append(snap.Reasons, fmt.Sprintf(
		"spool worker has not advanced for %s with %d bytes unprocessed (threshold %s)",
		stalled.Round(time.Second), snap.SpoolBacklogBytes, m.cfg.SpoolStallAfter))
}

// spoolLogAttrs are the spool fields every health log line carries.
func spoolLogAttrs(snap Snapshot) []any {
	return []any{
		"spool_backlog_known", snap.SpoolBacklogKnown,
		"spool_backlog_bytes", snap.SpoolBacklogBytes,
		"spool_stalled_seconds", snap.SpoolStalledSeconds,
		"spool_last_advance_at", snap.SpoolLastAdvanceAt,
	}
}
