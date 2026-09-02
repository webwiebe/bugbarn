package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// projectNameColWidth is the slug column in the switcher, so the human-readable
// names beside them line up.
const projectNameColWidth = 28

// projectOption is one row of the project switcher. An empty slug is the scope
// the TUI was launched with — every project, or the members of a --group.
type projectOption struct {
	slug  string
	label string
	name  string
}

// baseScopeLabel names the "no individual project" entry, which is not always
// "all projects": a --group launch stays inside its group.
func (m model) baseScopeLabel() string {
	if m.base.group != "" {
		return "all projects in group " + m.base.group
	}
	return "all projects"
}

// scopeLabel describes the scope currently in effect, for the list header.
func (m model) scopeLabel() string {
	if m.projSlug != "" {
		return m.projSlug
	}
	return m.baseScopeLabel()
}

func (m model) projectOptions() []projectOption {
	opts := make([]projectOption, 0, len(m.projects)+1)
	opts = append(opts, projectOption{label: m.baseScopeLabel()})
	for _, p := range m.projects {
		opts = append(opts, projectOption{slug: p.Slug, label: p.Slug, name: p.Name})
	}
	return opts
}

// filteredProjectOptions applies the picker's type-to-filter query against both
// the slug and the human-readable project name.
func (m model) filteredProjectOptions() []projectOption {
	all := m.projectOptions()
	q := strings.ToLower(strings.TrimSpace(m.projQuery))
	if q == "" {
		return all
	}
	matches := make([]projectOption, 0, len(all))
	for _, o := range all {
		if strings.Contains(strings.ToLower(o.label), q) || strings.Contains(strings.ToLower(o.name), q) {
			matches = append(matches, o)
		}
	}
	return matches
}

func (m model) openProjectPicker() model {
	if len(m.projects) == 0 {
		return m
	}
	m.view = viewProjects
	m.projQuery = ""
	m.projCursor = 0
	for i, o := range m.projectOptions() {
		if o.slug == m.projSlug {
			m.projCursor = i
			break
		}
	}
	return m
}

// cycleProject jumps straight to the next (or previous) scope without opening
// the picker — the fast path for flipping between a handful of projects.
func (m model) cycleProject(delta int) (model, tea.Cmd) {
	opts := m.projectOptions()
	if len(opts) < 2 {
		return m, nil
	}
	i := 0
	for idx, o := range opts {
		if o.slug == m.projSlug {
			i = idx
			break
		}
	}
	i = ((i+delta)%len(opts) + len(opts)) % len(opts)
	return m.selectProject(opts[i].slug)
}

// selectProject rescopes the client and reloads the list. An empty slug returns
// to the launch scope.
func (m model) selectProject(slug string) (model, tea.Cmd) {
	m.projSlug = slug
	if slug == "" {
		m.client = m.base
	} else {
		m.client = m.base.withProject(slug)
	}
	m.view = viewList
	m.cursor = 0
	m.err = nil
	m.loading = true
	return m, m.fetchIssues()
}

func (m model) handleProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.view = viewList
		return m, nil
	case tea.KeyEnter:
		return m.applyProjectSelection()
	case tea.KeyUp, tea.KeyCtrlP:
		if m.projCursor > 0 {
			m.projCursor--
		}
	case tea.KeyDown, tea.KeyCtrlN:
		if m.projCursor < len(m.filteredProjectOptions())-1 {
			m.projCursor++
		}
	case tea.KeyBackspace:
		m.projQuery = trimLastRune(m.projQuery)
		m.projCursor = 0
	case tea.KeySpace:
		m.projQuery += " "
		m.projCursor = 0
	case tea.KeyRunes:
		m.projQuery += string(msg.Runes)
		m.projCursor = 0
	}
	return m, nil
}

func (m model) applyProjectSelection() (tea.Model, tea.Cmd) {
	opts := m.filteredProjectOptions()
	if m.projCursor < 0 || m.projCursor >= len(opts) {
		m.view = viewList
		return m, nil
	}
	return m.selectProject(opts[m.projCursor].slug)
}

func trimLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

func (m model) viewProjects() string {
	header := headerStyle.Width(m.width).Render("Switch project")
	opts := m.filteredProjectOptions()

	var rows []string
	if m.projQuery != "" {
		rows = append(rows, normalStyle.Render(labelStyle.Render("filter: ")+m.projQuery))
	}
	rows = append(rows, m.projectRows(opts)...)

	footer := footerStyle.Width(m.width).Render(
		helpItem("↑/↓", "select") + "  " +
			helpItem("enter", "apply") + "  " +
			helpItem("type", "filter") + "  " +
			helpItem("esc", "cancel"))

	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// projectRows renders the visible slice of the option list, scrolled to keep
// the cursor on screen.
func (m model) projectRows(opts []projectOption) []string {
	if len(opts) == 0 {
		return []string{normalStyle.Render(dimStyle.Render("no project matches that filter"))}
	}
	maxVisible := max(1, m.height-5)
	start := 0
	if m.projCursor >= maxVisible {
		start = m.projCursor - maxVisible + 1
	}

	rows := make([]string, 0, maxVisible)
	for i := start; i < len(opts) && i < start+maxVisible; i++ {
		rows = append(rows, m.projectRow(opts[i], i == m.projCursor))
	}
	return rows
}

func (m model) projectRow(o projectOption, selected bool) string {
	marker := " "
	if o.slug == m.projSlug {
		marker = "●"
	}
	line := statusResolved.Render(marker) + " " + padRight(o.label, projectNameColWidth, projectStyle)
	if o.name != "" && o.name != o.label {
		line += "  " + dimStyle.Render(o.name)
	}
	if selected {
		return selectedStyle.Width(max(1, m.width-2)).Render(line)
	}
	return normalStyle.Render(line)
}
