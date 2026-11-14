package domain

// ProjectUsage holds per-project usage counts.
type ProjectUsage struct {
	ProjectID  int64 `json:"project_id"`
	IssueCount int   `json:"issue_count"`
	EventCount int   `json:"event_count"`
	LogCount   int   `json:"log_count"`
	// IssueStatus splits IssueCount by status.
	IssueStatus IssueCounts `json:"issue_status"`
}

// ProjectActivity counts, per active project, the issues last seen recently and
// earlier in the lookback window.
type ProjectActivity struct {
	ProjectID int64
	Slug      string
	// Recent counts issues last seen within silentFor of now.
	Recent int
	// Earlier counts issues last seen between lookback+silentFor and silentFor ago.
	Earlier int
}
