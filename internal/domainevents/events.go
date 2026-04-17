package domainevents

import "github.com/wiebe-xyz/bugbarn/internal/storage"

// IssueCreated is published when a brand-new issue is persisted.
type IssueCreated struct {
	Issue     storage.Issue
	ProjectID int64
}

// IssueRegressed is published when a previously-resolved issue receives a new event.
type IssueRegressed struct {
	Issue     storage.Issue
	ProjectID int64
}

// EventDeadLettered is published when an ingest record cannot be processed after all retries.
type EventDeadLettered struct {
	IngestID string
	Err      string
}
