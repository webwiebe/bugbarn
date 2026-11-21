package domain

// MergeReport says what a project merge moved, or would move on a dry run.
type MergeReport struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Environment string `json:"environment,omitempty"`
	DryRun      bool   `json:"dry_run"`
	// AlreadyMerged is true when the source slug is gone and already an alias of
	// the target: a re-run of a merge that ran. Every count is then zero.
	AlreadyMerged bool `json:"already_merged"`
	// Moved counts the source rows per project-owned table before the merge.
	// Rows that mergeDrop discards, and rows a collision leaves behind, are
	// part of the count: it is the size of the source, not of the result.
	Moved map[string]int64 `json:"moved"`
	// Folded counts the source issues whose fingerprint the target holds; they
	// fold into the target issue instead of moving.
	Folded int64 `json:"folded_issues"`
	// IssueNumberShift is how far the source's issue numbers move up.
	IssueNumberShift int64 `json:"issue_number_shift"`
	// TargetIssuesAfter is the target's issue count after the merge.
	TargetIssuesAfter int64 `json:"target_issues_after"`
}
