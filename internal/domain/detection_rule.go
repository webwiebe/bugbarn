package domain

import (
	"time"
)

// StoredDetectionRule is one row of detection_rules: the rule JSON as the API
// accepted it. The storage layer does not parse it; internal/detect owns the
// format.
type StoredDetectionRule struct {
	ID        string
	JSON      string
	UpdatedAt time.Time
}
