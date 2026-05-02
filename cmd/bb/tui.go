package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type issue struct {
	ID            string `json:"ID"`
	Title         string `json:"Title"`
	Status        string `json:"Status"`
	ExceptionType string `json:"ExceptionType"`
	EventCount    int    `json:"EventCount"`
	FirstSeen     string `json:"FirstSeen"`
	LastSeen      string `json:"LastSeen"`
	ProjectSlug   string `json:"project_slug"`
}

type issueDetail struct {
	issue
	FingerprintExplanation []string `json:"FingerprintExplanation"`
	RepresentativeEvent    struct {
		Message   string `json:"message"`
		Severity  string `json:"severity"`
		Exception struct {
			Type       string `json:"type"`
			Message    string `json:"message"`
			Stacktrace []struct {
				Function string `json:"function"`
				File     string `json:"file"`
				Line     int    `json:"line"`
			} `json:"stacktrace"`
		} `json:"exception"`
	} `json:"RepresentativeEvent"`
}

type view int

const (
	viewList view = iota
	viewDetail
)

type issuesMsg struct {
	issues []issue
	err    error
}

type detailMsg struct {
	detail issueDetail
	err    error
}

type actionMsg struct {
	err error
}

type model struct {
	client   *Client
	issues   []issue
	cursor   int
	view     view
	detail   issueDetail
	viewport viewport.Model
	width    int
	height   int
	err      error
	loading  bool
	filter   string
}

func newModel(client *Client, filter string) model {
	return model{
		client:  client,
		loading: true,
		filter:  filter,
	}
}

func (m model) Init() tea.Cmd {
	return m.fetchIssues()
}

func (m model) fetchIssues() tea.Cmd {
	return func() tea.Msg {
		params := "status=" + m.filter
		data, err := m.client.get("/api/v1/issues?" + params)
		if err != nil {
			return issuesMsg{err: err}
		}
		var resp struct {
			Issues []issue `json:"issues"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return issuesMsg{err: err}
		}
		return issuesMsg{issues: resp.Issues}
	}
}

func (m model) fetchDetail(id string) tea.Cmd {
	return func() tea.Msg {
		data, err := m.client.get("/api/v1/issues/" + id)
		if err != nil {
			return detailMsg{err: err}
		}
		var resp struct {
			Issue issueDetail `json:"issue"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return detailMsg{err: err}
		}
		return detailMsg{detail: resp.Issue}
	}
}

func (m model) resolveIssue(id string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.client.post("/api/v1/issues/"+id+"/resolve", nil)
		return actionMsg{err: err}
	}
}

func (m model) reopenIssue(id string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.client.post("/api/v1/issues/"+id+"/reopen", nil)
		return actionMsg{err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport = viewport.New(msg.Width, msg.Height-4)
		if m.view == viewDetail {
			m.viewport.SetContent(m.renderDetailContent())
		}
		return m, nil
	case issuesMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.issues = msg.issues
		return m, nil
	case detailMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.detail = msg.detail
		m.view = viewDetail
		m.viewport.SetContent(m.renderDetailContent())
		m.viewport.GotoTop()
		return m, nil
	case actionMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.view = viewList
		m.loading = true
		return m, m.fetchIssues()
	}
	if m.view == viewDetail {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	switch m.view {
	case viewList:
		return m.handleListKey(msg)
	case viewDetail:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.issues)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.issues) > 0 {
			m.loading = true
			return m, m.fetchDetail(m.issues[m.cursor].ID)
		}
	case "r":
		if len(m.issues) > 0 {
			iss := m.issues[m.cursor]
			if iss.Status == "unresolved" || iss.Status == "regressed" {
				return m, m.resolveIssue(iss.ID)
			} else if iss.Status == "resolved" {
				return m, m.reopenIssue(iss.ID)
			}
		}
	case "R":
		m.loading = true
		return m, m.fetchIssues()
	}
	return m, nil
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
	}
	return ""
}

func (m model) viewList() string {
	header := headerStyle.Width(m.width).Render(
		fmt.Sprintf("🐛 BugBarn Issues (%d)", len(m.issues)))

	if m.loading {
		content := lipgloss.Place(m.width, m.height-3, lipgloss.Center, lipgloss.Center, "Loading...")
		footer := footerStyle.Width(m.width).Render("q quit")
		return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
	}

	if m.err != nil {
		content := lipgloss.Place(m.width, m.height-3, lipgloss.Center, lipgloss.Center,
			lipgloss.NewStyle().Foreground(errorColor).Render("Error: "+m.err.Error()))
		footer := footerStyle.Width(m.width).Render("q quit")
		return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
	}

	if len(m.issues) == 0 {
		content := lipgloss.Place(m.width, m.height-3, lipgloss.Center, lipgloss.Center,
			lipgloss.NewStyle().Foreground(subtleColor).Render("No issues found"))
		footer := footerStyle.Width(m.width).Render(helpItem("q", "quit") + "  " + helpItem("R", "refresh"))
		return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
	}

	var rows []string
	maxVisible := m.height - 4
	start := 0
	if m.cursor >= maxVisible {
		start = m.cursor - maxVisible + 1
	}

	for i := start; i < len(m.issues) && i < start+maxVisible; i++ {
		iss := m.issues[i]
		icon := statusStyle(iss.Status).Render(statusIcon(iss.Status))
		title := iss.Title
		if len(title) > m.width-40 && m.width > 50 {
			title = title[:m.width-43] + "..."
		}
		proj := projectStyle.Render(iss.ProjectSlug)
		count := countStyle.Render(fmt.Sprintf("(%d)", iss.EventCount))
		ago := timeStyle.Render(timeAgo(iss.LastSeen))

		line := fmt.Sprintf("%s %s %s %s  %s", icon, title, proj, count, ago)
		if i == m.cursor {
			rows = append(rows, selectedStyle.Width(m.width-2).Render(line))
		} else {
			rows = append(rows, normalStyle.Render(line))
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, rows...)
	footer := footerStyle.Width(m.width).Render(
		helpItem("↑/↓", "navigate") + "  " +
			helpItem("enter", "detail") + "  " +
			helpItem("r", "resolve/reopen") + "  " +
			helpItem("R", "refresh") + "  " +
			helpItem("q", "quit"))

	return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
}

func (m model) viewDetail() string {
	title := m.detail.Title
	if len(title) > m.width-10 {
		title = title[:m.width-13] + "..."
	}
	header := headerStyle.Width(m.width).Render(
		statusStyle(m.detail.Status).Render(statusIcon(m.detail.Status)) + " " + title)

	footer := footerStyle.Width(m.width).Render(
		helpItem("esc", "back") + "  " +
			helpItem("↑/↓", "scroll") + "  " +
			helpItem("q", "quit"))

	m.viewport.Width = m.width
	m.viewport.Height = m.height - 3

	return lipgloss.JoinVertical(lipgloss.Left, header, m.viewport.View(), footer)
}

func (m model) renderDetailContent() string {
	d := m.detail
	var b strings.Builder

	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render("Issue") + "\n")
	b.WriteString(fmt.Sprintf("  ID:        %s\n", d.ID))
	b.WriteString(fmt.Sprintf("  Status:    %s\n", statusStyle(d.Status).Render(d.Status)))
	b.WriteString(fmt.Sprintf("  Project:   %s\n", projectStyle.Render(d.ProjectSlug)))
	b.WriteString(fmt.Sprintf("  Events:    %d\n", d.EventCount))
	b.WriteString(fmt.Sprintf("  First:     %s\n", timeStyle.Render(d.FirstSeen)))
	b.WriteString(fmt.Sprintf("  Last:      %s\n", timeStyle.Render(d.LastSeen)))
	if d.ExceptionType != "" {
		b.WriteString(fmt.Sprintf("  Exception: %s\n", lipgloss.NewStyle().Foreground(errorColor).Render(d.ExceptionType)))
	}

	exc := d.RepresentativeEvent.Exception
	if exc.Message != "" {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render("Exception") + "\n")
		b.WriteString(fmt.Sprintf("  %s: %s\n", exc.Type, exc.Message))
	}

	if len(exc.Stacktrace) > 0 {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render("Stack Trace") + "\n")
		for i, frame := range exc.Stacktrace {
			if i >= 15 {
				b.WriteString(fmt.Sprintf("  ... and %d more frames\n", len(exc.Stacktrace)-15))
				break
			}
			fn := frame.Function
			if fn == "" {
				fn = "<anonymous>"
			}
			loc := frame.File
			if frame.Line > 0 {
				loc = fmt.Sprintf("%s:%d", frame.File, frame.Line)
			}
			b.WriteString(fmt.Sprintf("  %s %s\n",
				lipgloss.NewStyle().Foreground(accentColor).Render(fn),
				timeStyle.Render(loc)))
		}
	}

	if len(d.FingerprintExplanation) > 0 {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render("Grouping") + "\n")
		for _, line := range d.FingerprintExplanation {
			b.WriteString(fmt.Sprintf("  %s\n", line))
		}
	}

	return b.String()
}

func timeAgo(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

func runTUI(client *Client, filter string) error {
	m := newModel(client, filter)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
