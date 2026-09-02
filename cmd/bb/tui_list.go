package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (m model) viewList() string {
	header := m.listHeader()

	switch {
	case m.loading:
		return m.listMessage(header, "Loading...", helpItem("q", "quit"))
	case m.err != nil:
		msg := lipgloss.NewStyle().Foreground(errorColor).Render("Error: " + m.err.Error())
		return m.listMessage(header, msg, m.listFooter())
	case len(m.issues) == 0:
		msg := dimStyle.Render(fmt.Sprintf("No %s issues in %s", m.status, m.scopeLabel()))
		return m.listMessage(header, msg, m.listFooter())
	}

	content := lipgloss.JoinVertical(lipgloss.Left, m.issueRows()...)
	return lipgloss.JoinVertical(lipgloss.Left, header, content, m.listFooter())
}

// listHeader shows the count together with the scope and status filter, so it
// is always obvious which slice of the instance you are looking at.
func (m model) listHeader() string {
	scope := scopeStyle.Render(m.scopeLabel())
	text := fmt.Sprintf("🐛 BugBarn · %d issues · %s · %s", len(m.issues), scope, m.status)
	return headerStyle.Width(m.width).Render(truncate(text, max(1, m.width-4)))
}

func (m model) listFooter() string {
	return footerStyle.Width(m.width).Render(
		helpItem("↑/↓", "navigate") + "  " +
			helpItem("enter", "detail") + "  " +
			helpItem("p", "project") + "  " +
			helpItem("tab", "cycle") + "  " +
			helpItem("s", "status") + "  " +
			helpItem("v", "vibe") + "  " +
			helpItem("r", "resolve") + "  " +
			helpItem("R", "refresh") + "  " +
			helpItem("q", "quit"))
}

func (m model) listMessage(header, body, footer string) string {
	content := lipgloss.Place(m.width, max(1, m.height-3), lipgloss.Center, lipgloss.Center, body)
	return lipgloss.JoinVertical(lipgloss.Left, header, content, footerStyle.Width(m.width).Render(footer))
}

func (m model) issueRows() []string {
	maxVisible := max(1, m.height-4)
	start := 0
	if m.cursor >= maxVisible {
		start = m.cursor - maxVisible + 1
	}

	rows := make([]string, 0, maxVisible)
	for i := start; i < len(m.issues) && i < start+maxVisible; i++ {
		rows = append(rows, m.issueRow(m.issues[i], i == m.cursor))
	}
	return rows
}

// Fixed widths for the trailing columns, so counts and ages line up down the
// list instead of trailing raggedly behind titles of every length.
const (
	projectColWidth = 20
	countColWidth   = 7
	ageColWidth     = 9
)

// issueRow renders one list line. The project column is dropped while a single
// project is selected — repeating the same slug on every row only eats the
// width the title needs.
func (m model) issueRow(iss domain.Issue, selected bool) string {
	suffix := padLeft(fmt.Sprintf("(%d)", iss.EventCount), countColWidth, countStyle) +
		padLeft(timeAgo(iss.LastSeen), ageColWidth, timeStyle)
	if m.projSlug == "" {
		suffix = " " + padRight(truncate(iss.ProjectSlug, projectColWidth), projectColWidth, projectStyle) + suffix
	}

	// 6 cells of chrome: the status icon, its separator, and the selection
	// border plus padding lipgloss adds around the row.
	room := max(10, m.width-lipgloss.Width(suffix)-6)
	line := fmt.Sprintf("%s %s%s",
		statusStyle(iss.Status).Render(statusIcon(iss.Status)),
		padRight(truncate(iss.Title, room), room, lipgloss.NewStyle()),
		suffix)

	if selected {
		return selectedStyle.Width(max(1, m.width-2)).Render(line)
	}
	return normalStyle.Render(line)
}

// padRight/padLeft size a column before styling it, so the ANSI escapes the
// style adds never count towards the column width.
func padRight(s string, width int, style lipgloss.Style) string {
	return style.Render(s) + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}

func padLeft(s string, width int, style lipgloss.Style) string {
	return strings.Repeat(" ", max(0, width-lipgloss.Width(s))) + style.Render(s)
}
