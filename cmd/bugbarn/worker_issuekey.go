package main

import (
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
)

// issueKey is the grouping key of a processed event: its fingerprint, or the
// recomputed fingerprint when the record carries none. The one-shot worker
// counts distinct keys to report how many issues a batch touched.
func issueKey(item event.ProcessedEvent) string {
	if key := strings.TrimSpace(item.Fingerprint); key != "" {
		return key
	}
	return fingerprint.Fingerprint(item.Event)
}
