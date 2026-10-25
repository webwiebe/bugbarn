package main

import (
	"encoding/json"
	"fmt"
)

// requireIssueStatus fails when a mute or unmute answered 200 with an issue that
// is not in the status the command asked for, so a server that does not apply
// the change cannot print success (#232). A queued mutation (HTTP 202,
// {"queued":true}) has no issue yet and passes.
func requireIssueStatus(data []byte, issueID, want string) error {
	var resp struct {
		Queued bool `json:"queued"`
		Issue  *struct {
			Status string `json:"status"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("%s: unreadable response: %w", issueID, err)
	}
	if resp.Queued {
		return nil
	}
	if resp.Issue == nil {
		return fmt.Errorf("%s: the response carries no issue, so the change is unconfirmed", issueID)
	}
	if resp.Issue.Status != want {
		return fmt.Errorf("%s is %q after the request, expected %q", issueID, resp.Issue.Status, want)
	}
	return nil
}
