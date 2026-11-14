package ingesthealth

import "time"

// Config holds the monitor's sampling cadence and the thresholds that flip the
// pipeline to unhealthy. Zero values fall back to the defaults in New.
type Config struct {
	// Interval is how often the monitor samples. Default 60s.
	Interval time.Duration
	// StaleAfter is the maximum age of the most recent persisted event before a
	// backed-up queue is considered a stall. Default 30m; wired to
	// BUGBARN_INGEST_STALE_AFTER_SECONDS so it can be raised on an environment
	// whose quiet periods run long (e.g. an idle testing box that has no write
	// queue to distinguish idle from stalled — see evaluate). Staleness alone
	// never flips a queue-backed instance unhealthy; the queue depth must
	// corroborate it.
	StaleAfter time.Duration
	// SpoolStallAfter is how long the writer spool may hold unprocessed bytes
	// without the worker's cursor moving before the worker counts as stalled.
	// Default 30m. Separate from StaleAfter, which idle instances raise; see
	// spool.go.
	SpoolStallAfter time.Duration
	// MaxQueueDepth is the write-queue backlog (entries) above which ingest is
	// considered backed up. Default 50_000. Zero disables the check.
	MaxQueueDepth int64
	// WarnWALBytes is the WAL size above which a warning is logged (the WAL not
	// truncating is the leading indicator of checkpoint contention). It does not
	// by itself mark the pipeline unhealthy. Default 256 MiB. Zero disables.
	WarnWALBytes int64
	// AlertEvery throttles repeated unhealthy logs for the same condition.
	// Default 15m.
	AlertEvery time.Duration
	// NotifyTimeout bounds a single out-of-band delivery round. Default 30s.
	NotifyTimeout time.Duration
	// Environment (production/staging/testing) labels outgoing alerts so a
	// recipient can tell which instance is stalled. Optional.
	Environment string
	// ConfirmSamples is how many consecutive samples must find the pipeline
	// unhealthy before the snapshot reports it and the alert fires. Default 3
	// (two minutes at the default interval). One sample is not enough: the
	// write queue also carries logs, security and metrics batches, so it is
	// briefly non-empty whenever the writer restarts during a rollout, and a
	// production instance can go half an hour without an error event. A new
	// pod's first sample then saw "events queued but not persisted" for a few
	// seconds and paged (BS2-107). A real stall holds across samples.
	ConfirmSamples int
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	if c.StaleAfter <= 0 {
		c.StaleAfter = 30 * time.Minute
	}
	if c.SpoolStallAfter <= 0 {
		c.SpoolStallAfter = 30 * time.Minute
	}
	if c.MaxQueueDepth == 0 {
		c.MaxQueueDepth = 50_000
	}
	if c.WarnWALBytes == 0 {
		c.WarnWALBytes = 256 << 20
	}
	if c.AlertEvery <= 0 {
		c.AlertEvery = 15 * time.Minute
	}
	if c.NotifyTimeout <= 0 {
		c.NotifyTimeout = 30 * time.Second
	}
	if c.ConfirmSamples <= 0 {
		c.ConfirmSamples = 3
	}
	return c
}

// Snapshot is the most recent sample. It is safe to read concurrently.
type Snapshot struct {
	// Sampled is false until the first sample completes.
	Sampled             bool      `json:"sampled"`
	Healthy             bool      `json:"healthy"`
	Reasons             []string  `json:"reasons,omitempty"`
	SampledAt           time.Time `json:"sampledAt"`
	LastEventAt         time.Time `json:"lastEventAt"`
	LastEventAgeSeconds float64   `json:"lastEventAgeSeconds"`
	HasEvents           bool      `json:"hasEvents"`
	QueueDepth          int64     `json:"queueDepth"`
	QueueDepthKnown     bool      `json:"queueDepthKnown"`
	WALSizeBytes        int64     `json:"walSizeBytes"`
	// UnhealthySamples counts the consecutive samples, this one included,
	// that found the pipeline unhealthy. Healthy turns false once it reaches
	// Config.ConfirmSamples.
	UnhealthySamples int `json:"unhealthySamples,omitempty"`
	// SpoolBacklogBytes is what the writer's spool worker has not processed
	// yet; SpoolStalledSeconds is how long that backlog has waited without the
	// worker's cursor moving (0 when the spool is empty). See spool.go.
	SpoolBacklogKnown   bool      `json:"spoolBacklogKnown"`
	SpoolBacklogBytes   int64     `json:"spoolBacklogBytes"`
	SpoolStalledSeconds float64   `json:"spoolStalledSeconds"`
	SpoolLastAdvanceAt  time.Time `json:"spoolLastAdvanceAt"`
}
