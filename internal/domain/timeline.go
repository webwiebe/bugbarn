package domain

import (
	"time"
)

// TimelineRow is the error-event weight of one project in one UTC hour.
type TimelineRow struct {
	ProjectID   int64
	ProjectSlug string
	ProjectName string
	Hour        time.Time
	Count       int64
}
