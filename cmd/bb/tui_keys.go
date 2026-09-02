package main

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// statusFilters is the cycle the `s` key walks through — the same set the
// --status flag accepts.
var statusFilters = []string{"open", "resolved", "muted", "all"}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.view == viewProjects {
		return m.handleProjectKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.view == viewDetail {
			m.view = viewList
			m.err = nil
			return m, nil
		}
		return m, tea.Quit
	}
	if m.view == viewDetail {
		return m.handleDetailKey(msg)
	}
	return m.handleListKey(msg)
}

func (m model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if handled, next, cmd := m.handleScopeKey(msg); handled {
		return next, cmd
	}
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.cursor < len(m.issues)-1 {
			m.cursor++
		}
		return m, nil
	case "R":
		m.loading = true
		return m, m.fetchIssues()
	}
	if len(m.issues) == 0 {
		return m, nil
	}
	return m.handleIssueKey(msg, m.issues[m.cursor])
}

// handleIssueKey owns the keys that act on the highlighted issue; the caller
// has already established that there is one.
func (m model) handleIssueKey(msg tea.KeyMsg, iss domain.Issue) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.loading = true
		return m, m.fetchDetail(iss.ID)
	case "v":
		m.loading = true
		return m, m.prepareVibe(iss.ID)
	case "r":
		return m.toggleResolve(iss)
	}
	return m, nil
}

// handleScopeKey owns the keys that change *what* is listed rather than move
// around it. The bool reports whether the key was consumed.
func (m model) handleScopeKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	switch msg.String() {
	case "p":
		return true, m.openProjectPicker(), nil
	case "tab":
		next, cmd := m.cycleProject(1)
		return true, next, cmd
	case "shift+tab":
		next, cmd := m.cycleProject(-1)
		return true, next, cmd
	case "s":
		next, cmd := m.cycleStatus()
		return true, next, cmd
	}
	return false, m, nil
}

func (m model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "v":
		if m.detail.ID == "" {
			return m, nil
		}
		m.loading = true
		return m, m.prepareVibe(m.detail.ID)
	case "right", "l", "]", "n":
		return m.stepOccurrence(1), nil
	case "left", "h", "[", "N":
		return m.stepOccurrence(-1), nil
	case "r":
		if m.detail.ID == "" {
			return m, nil
		}
		return m.toggleResolve(m.detail)
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// stepOccurrence moves through the issue's recent events, wrapping at both ends.
func (m model) stepOccurrence(delta int) model {
	if len(m.events) < 2 {
		return m
	}
	m.eventIdx = (m.eventIdx + delta + len(m.events)) % len(m.events)
	m.refreshDetail()
	m.viewport.GotoTop()
	return m
}

// toggleResolve flips an issue between its open and closed states: resolve what
// is open, reopen what was resolved, unmute what was muted.
func (m model) toggleResolve(iss domain.Issue) (tea.Model, tea.Cmd) {
	switch iss.Status {
	case "unresolved", "regressed":
		return m, m.resolveIssue(iss.ID)
	case "resolved":
		return m, m.reopenIssue(iss.ID)
	case "muted":
		return m, m.unmuteIssue(iss.ID)
	}
	return m, nil
}

func (m model) cycleStatus() (model, tea.Cmd) {
	i := indexOfString(statusFilters, m.status)
	m.status = statusFilters[(i+1)%len(statusFilters)]
	m.cursor = 0
	m.loading = true
	m.err = nil
	return m, m.fetchIssues()
}

// indexOfString returns the position of v in list, or 0 when it is absent —
// an unknown current value simply restarts the cycle from the top.
func indexOfString(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}
