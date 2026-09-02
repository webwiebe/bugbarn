package main

import (
	"os/exec"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// view is the screen the TUI is currently showing.
type view int

const (
	viewList view = iota
	viewDetail
	viewProjects
)

type issuesMsg struct {
	issues []domain.Issue
	err    error
}

type detailMsg struct {
	detail domain.Issue
	err    error
}

// eventsMsg carries the recent occurrences of an issue. issueID is echoed back
// so a response that lands after the user has navigated on is dropped instead
// of overwriting the occurrences of the issue now on screen.
type eventsMsg struct {
	issueID string
	events  []domain.Event
	err     error
}

type projectsMsg struct {
	projects []project
	err      error
}

type actionMsg struct{ err error }

// vibeMsg carries a prepared Claude command to hand off to tea.ExecProcess.
type vibeMsg struct {
	cmd *exec.Cmd
	err error
}

// vibeDoneMsg fires when the suspended Claude session returns to the TUI.
type vibeDoneMsg struct{ err error }

type model struct {
	// base is the client as launched, minus any project scope, so every project
	// switch starts from the same baseline (and a --group launch keeps its
	// group). client is base narrowed to projSlug, or base itself when no
	// individual project is selected.
	base   *Client
	client *Client

	issues []domain.Issue
	cursor int
	view   view
	status string // issue status filter: open|resolved|muted|all

	detail        domain.Issue
	events        []domain.Event
	eventIdx      int
	eventsLoading bool
	eventsErr     error

	projects   []project
	projCursor int
	projQuery  string
	projSlug   string // "" = the scope the TUI was launched with

	viewport viewport.Model
	width    int
	height   int
	err      error
	loading  bool
}

func newModel(client *Client, status string) model {
	base := client
	if client.project != "" {
		// A --project launch is just a preselected switcher entry: keep the
		// baseline unscoped so the user can still cycle to the other projects.
		base = client.withProject("")
	}
	return model{
		base:     base,
		client:   client,
		status:   status,
		projSlug: client.project,
		loading:  true,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.fetchIssues(), m.fetchProjects())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		return m.handleResize(msg)
	}
	if handled, next, cmd := m.handleDataMsg(msg); handled {
		return next, cmd
	}
	if m.view == viewDetail {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.viewport = viewport.New(msg.Width, msg.Height-4)
	if m.view == viewDetail {
		m.refreshDetail()
	}
	return m, nil
}

// handleDataMsg applies the async responses. The bool reports whether the
// message was one of ours, so Update can fall through to the viewport.
func (m model) handleDataMsg(msg tea.Msg) (bool, tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case issuesMsg:
		return true, m.applyIssues(msg), nil
	case detailMsg:
		next, cmd := m.applyDetail(msg)
		return true, next, cmd
	case eventsMsg:
		return true, m.applyEvents(msg), nil
	case projectsMsg:
		return true, m.applyProjects(msg), nil
	case actionMsg:
		next, cmd := m.applyAction(msg)
		return true, next, cmd
	case vibeMsg:
		next, cmd := m.applyVibe(msg)
		return true, next, cmd
	case vibeDoneMsg:
		return true, m.applyVibeDone(msg), nil
	}
	return false, m, nil
}

func (m model) applyIssues(msg issuesMsg) model {
	m.loading = false
	m.err = msg.err
	if msg.err != nil {
		return m
	}
	m.issues = msg.issues
	if m.cursor >= len(m.issues) {
		m.cursor = max(0, len(m.issues)-1)
	}
	return m
}

func (m model) applyDetail(msg detailMsg) (model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		return m, nil
	}
	m.err = nil
	m.detail = msg.detail
	m.events, m.eventIdx, m.eventsErr = nil, 0, nil
	m.eventsLoading = true
	m.view = viewDetail
	m.refreshDetail()
	m.viewport.GotoTop()
	return m, m.fetchEvents(msg.detail.ID)
}

func (m model) applyEvents(msg eventsMsg) model {
	if msg.issueID != m.detail.ID {
		return m
	}
	m.eventsLoading = false
	m.eventsErr = msg.err
	m.events = msg.events
	m.eventIdx = 0
	m.refreshDetail()
	return m
}

func (m model) applyProjects(msg projectsMsg) model {
	if msg.err != nil {
		// A failed project list only disables the switcher; the issue list the
		// user came for is unaffected, so this must not clobber m.err.
		return m
	}
	m.projects = msg.projects
	return m
}

func (m model) applyAction(msg actionMsg) (model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err
		return m, nil
	}
	m.view = viewList
	m.loading = true
	return m, m.fetchIssues()
}

func (m model) applyVibe(msg vibeMsg) (model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		return m, nil
	}
	// Suspend the TUI, hand the terminal to Claude, resume when it exits.
	return m, tea.ExecProcess(msg.cmd, func(err error) tea.Msg {
		return vibeDoneMsg{err: err}
	})
}

func (m model) applyVibeDone(msg vibeDoneMsg) model {
	// A non-zero exit just means the Claude session ended — not an error.
	if _, ok := msg.err.(*exec.ExitError); !ok && msg.err != nil {
		m.err = msg.err
	}
	return m
}

func (m model) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	switch m.view {
	case viewList:
		return m.viewList()
	case viewDetail:
		return m.viewDetail()
	case viewProjects:
		return m.viewProjects()
	}
	return ""
}

func runTUI(client *Client, status string) error {
	p := tea.NewProgram(newModel(client, status), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
