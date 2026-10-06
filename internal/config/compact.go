package config

import (
	"os"
	"strings"
)

// Compact configures the one-time storage compaction the writer runs at start.
type Compact struct {
	// OnStart is BUGBARN_COMPACT_ON_START. When "true", the writer rewrites
	// bugbarn.db once in auto_vacuum=INCREMENTAL mode at startup (see
	// storage.Compact) and deletes the shadow directory Litestream left
	// behind. Off by default; testing and staging turn it on.
	OnStart bool
	// MaxLiveBytes is BUGBARN_COMPACT_MAX_LIVE_BYTES (default 1GiB): the
	// compaction is skipped when the database holds more live data than this,
	// because VACUUM holds the single write connection while it runs.
	MaxLiveBytes int64
}

func parseCompactConfig() Compact {
	return Compact{
		OnStart:      strings.EqualFold(os.Getenv("BUGBARN_COMPACT_ON_START"), "true"),
		MaxLiveBytes: envInt64Positive("BUGBARN_COMPACT_MAX_LIVE_BYTES", 1<<30),
	}
}
