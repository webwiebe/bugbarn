package domainevents

import "github.com/wiebe-xyz/bugbarn/internal/domain"

// IssueCreated is published when a brand-new issue is persisted.
type IssueCreated struct {
	Issue     domain.Issue
	ProjectID int64
}

// IssueRegressed is published when a previously-resolved issue receives a new event.
type IssueRegressed struct {
	Issue     domain.Issue
	ProjectID int64
}

// IssueEventRecorded is published for every successfully persisted event,
// regardless of whether the issue is new or regressed. Used to evaluate
// event_count_exceeds alert conditions.
type IssueEventRecorded struct {
	Issue     domain.Issue
	ProjectID int64
}

// EventDeadLettered is published when an ingest record cannot be processed after all retries.
type EventDeadLettered struct {
	IngestID string
	Err      string
}
