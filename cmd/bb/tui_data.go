package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// occurrenceLimit is how many recent events the detail pane pulls per issue.
// Enough to page back through a burst without turning a keypress into a
// multi-megabyte response.
const occurrenceLimit = 50

// Every command captures the client it was created with rather than reading
// m.client from the goroutine: a project switch replaces the client, and an
// in-flight request must keep using the one it started with.

func (m model) fetchIssues() tea.Cmd {
	client, status := m.client, m.status
	return func() tea.Msg {
		params := url.Values{}
		params.Set("status", status)
		params.Set("sort", "last_seen")
		data, err := client.get("/api/v1/issues?" + params.Encode())
		if err != nil {
			return issuesMsg{err: err}
		}
		var resp struct {
			Issues []domain.Issue `json:"issues"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return issuesMsg{err: fmt.Errorf("parse issues response: %w", err)}
		}
		return issuesMsg{issues: resp.Issues}
	}
}

func (m model) fetchDetail(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		issue, err := fetchIssue(client, id)
		return detailMsg{detail: issue, err: err}
	}
}

func (m model) fetchEvents(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		path := fmt.Sprintf("/api/v1/issues/%s/events?limit=%d", id, occurrenceLimit)
		data, err := client.get(path)
		if err != nil {
			return eventsMsg{issueID: id, err: err}
		}
		var resp struct {
			Events []domain.Event `json:"events"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return eventsMsg{issueID: id, err: fmt.Errorf("parse events response: %w", err)}
		}
		return eventsMsg{issueID: id, events: resp.Events}
	}
}

func (m model) fetchProjects() tea.Cmd {
	client := m.base
	return func() tea.Msg {
		projects, err := listSwitchableProjects(client)
		return projectsMsg{projects: projects, err: err}
	}
}

// listSwitchableProjects returns the projects the switcher may select. When the
// TUI was launched with a group scope the list is narrowed to that group's
// members, so cycling never silently walks out of the group the user asked for.
func listSwitchableProjects(client *Client) ([]project, error) {
	// /api/v1/projects is unscoped server-side; ask with a bare client so the
	// group header can't influence the answer we then filter ourselves.
	data, err := client.withProject("").get("/api/v1/projects")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Projects []project `json:"projects"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse projects response: %w", err)
	}
	if client.group == "" {
		return resp.Projects, nil
	}
	id, err := groupID(client, client.group)
	if err != nil {
		return nil, err
	}
	return projectsInGroup(resp.Projects, id), nil
}

// groupID resolves a group slug to its numeric id.
func groupID(client *Client, slug string) (int64, error) {
	data, err := client.withProject("").get("/api/v1/groups")
	if err != nil {
		return 0, err
	}
	var resp struct {
		Groups []group `json:"groups"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, fmt.Errorf("parse groups response: %w", err)
	}
	for _, g := range resp.Groups {
		if g.Slug == slug {
			return g.ID, nil
		}
	}
	return 0, fmt.Errorf("group %q not found", slug)
}

func projectsInGroup(projects []project, id int64) []project {
	members := make([]project, 0, len(projects))
	for _, p := range projects {
		if p.GroupID != nil && *p.GroupID == id {
			members = append(members, p)
		}
	}
	return members
}

// prepareVibe fetches issue context and builds the Claude command off the UI
// thread; the resulting vibeMsg is handed to tea.ExecProcess in Update.
func (m model) prepareVibe(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		cmd, err := prepareVibeCommand(client, id)
		return vibeMsg{cmd: cmd, err: err}
	}
}

func (m model) resolveIssue(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_, err := client.post("/api/v1/issues/"+id+"/resolve", nil)
		return actionMsg{err: err}
	}
}

func (m model) reopenIssue(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_, err := client.post("/api/v1/issues/"+id+"/reopen", nil)
		return actionMsg{err: err}
	}
}

func (m model) unmuteIssue(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		_, err := client.patch("/api/v1/issues/"+id+"/unmute", nil)
		return actionMsg{err: err}
	}
}
