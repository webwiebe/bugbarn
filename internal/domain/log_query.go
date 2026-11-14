package domain

import (
	"time"
)

// LogAttrFilter keeps entries whose top-level attribute Key renders as Value.
type LogAttrFilter struct {
	Key   string
	Value string
}

// LogQuery holds every filter the log explorer applies. Zero fields do not filter.
type LogQuery struct {
	ProjectID int64
	LevelMin  int
	Q         string
	// From and To bound received_at, From inclusive and To exclusive.
	From, To time.Time
	Attrs    []LogAttrFilter
	BeforeID int64
}

// LogHistogramBucket counts the entries of one time bucket per level name.
type LogHistogramBucket struct {
	Start  time.Time      `json:"start"`
	Counts map[string]int `json:"counts"`
}

// LogHistogram is the volume chart for a LogQuery.
type LogHistogram struct {
	From          time.Time            `json:"from"`
	To            time.Time            `json:"to"`
	BucketSeconds int                  `json:"bucket_seconds"`
	Buckets       []LogHistogramBucket `json:"buckets"`
	// Truncated is true when the scan window cut off rows inside the range.
	Truncated bool `json:"truncated"`
}

// LogFacetCount is one attribute key or value with the number of entries carrying it.
type LogFacetCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// LogFacets is the facet answer: attribute keys, or the top values of one key.
type LogFacets struct {
	Key       string          `json:"key,omitempty"`
	Items     []LogFacetCount `json:"items"`
	Truncated bool            `json:"truncated"`
}
