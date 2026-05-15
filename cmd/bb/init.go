package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const localConfigFile = ".bugbarn.json"

type LocalConfig struct {
	Project string `json:"project,omitempty"`
	Group   string `json:"group,omitempty"`
}

func findLocalConfig() (LocalConfig, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return LocalConfig{}, false
	}
	for {
		p := filepath.Join(dir, localConfigFile)
		data, err := os.ReadFile(p)
		if err == nil {
			var lc LocalConfig
			if json.Unmarshal(data, &lc) == nil && (lc.Project != "" || lc.Group != "") {
				return lc, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return LocalConfig{}, false
}

func saveLocalConfig(lc LocalConfig) error {
	data, err := json.MarshalIndent(lc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(localConfigFile, append(data, '\n'), 0644)
}

// --- domain types ---

type project struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type group struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// --- TUI model ---

type initItem struct {
	slug    string
	name    string
	isGroup bool
	header  string // non-empty → section label, not selectable
}

type initDataMsg struct {
	groups   []group
	projects []project
	err      error
}

type initModel struct {
	client        *Client
	items         []initItem
	cursor        int // index into items (selectable items only)
	err           error
	loading       bool
	chosen        string
	chosenIsGroup bool
}

func newInitModel(client *Client) initModel {
	return initModel{client: client, loading: true}
}

func (m initModel) Init() tea.Cmd {
	return func() tea.Msg {
		var groups []group
		if data, err := m.client.get("/api/v1/groups"); err == nil {
			var resp struct {
				Groups []group `json:"groups"`
			}
			if json.Unmarshal(data, &resp) == nil {
				groups = resp.Groups
			}
		}

		data, err := m.client.get("/api/v1/projects")
		if err != nil {
			return initDataMsg{err: err}
		}
		var resp struct {
			Projects []project `json:"projects"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return initDataMsg{err: err}
		}
		return initDataMsg{groups: groups, projects: resp.Projects}
	}
}

func buildItems(groups []group, projects []project) []initItem {
	var items []initItem
	if len(groups) > 0 {
		items = append(items, initItem{header: "Groups"})
		for _, g := range groups {
			items = append(items, initItem{slug: g.Slug, name: g.Name, isGroup: true})
		}
		items = append(items, initItem{header: "─"})
	}
	items = append(items, initItem{header: "Projects"})
	for _, p := range projects {
		items = append(items, initItem{slug: p.Slug, name: p.Name})
	}
	return items
}

func (m initModel) selectableCount() int {
	n := 0
	for _, item := range m.items {
		if item.header == "" {
			n++
		}
	}
	return n
}

// selectableIndex returns the items[] index of the n-th selectable item.
func selectableIndex(items []initItem, n int) int {
	count := 0
	for i, item := range items {
		if item.header == "" {
			if count == n {
				return i
			}
			count++
		}
	}
	return len(items) - 1
}

func (m initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case initDataMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		if len(msg.projects) == 0 {
			m.err = fmt.Errorf("no projects found — create one with: bb projects --create \"My App\"")
			return m, tea.Quit
		}
		m.items = buildItems(msg.groups, msg.projects)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < m.selectableCount()-1 {
				m.cursor++
			}
		case "enter":
			idx := selectableIndex(m.items, m.cursor)
			item := m.items[idx]
			m.chosen = item.slug
			m.chosenIsGroup = item.isGroup
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m initModel) View() string {
	if m.loading {
		return "\n  Loading…\n"
	}
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n", m.err)
	}
	if m.chosen != "" {
		return ""
	}

	sectionStyle := lipgloss.NewStyle().Foreground(subtleColor).PaddingLeft(2)
	dividerStyle := lipgloss.NewStyle().Foreground(subtleColor).PaddingLeft(2)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(" bb init "))
	b.WriteString("\n\n")

	selIdx := 0
	for _, item := range m.items {
		if item.header == "─" {
			b.WriteString(dividerStyle.Render("──────────────────────────────"))
			b.WriteString("\n")
			continue
		}
		if item.header != "" {
			b.WriteString(sectionStyle.Render(strings.ToUpper(item.header)))
			b.WriteString("\n")
			continue
		}

		var tag string
		if item.isGroup {
			tag = " group"
		}
		line := fmt.Sprintf("%-22s %s%s", item.slug, item.name, tag)
		if selIdx == m.cursor {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(normalStyle.Render(
				lipgloss.NewStyle().Foreground(subtleColor).Render(line),
			))
		}
		b.WriteString("\n")
		selIdx++
	}

	b.WriteString("\n")
	b.WriteString(footerStyle.Render(
		helpItem("↑/↓", "navigate") + "  " + helpItem("enter", "select") + "  " + helpItem("q", "quit"),
	))
	b.WriteString("\n")
	return b.String()
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project slug (skip interactive picker)")
	groupFlag := fs.String("group", "", "group slug (skip interactive picker)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *projectFlag != "" && *groupFlag != "" {
		return fmt.Errorf("--project and --group are mutually exclusive")
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	// Non-interactive: --group flag
	if *groupFlag != "" {
		if err := validateGroup(client, *groupFlag); err != nil {
			return err
		}
		if err := saveLocalConfig(LocalConfig{Group: *groupFlag}); err != nil {
			return fmt.Errorf("write %s: %w", localConfigFile, err)
		}
		fmt.Fprintf(os.Stderr, "Group set to %q — saved to %s\n", *groupFlag, localConfigFile)
		writeOut(map[string]any{"ok": true, "group": *groupFlag, "file": localConfigFile})
		return nil
	}

	// Non-interactive: --project flag
	if *projectFlag != "" {
		if err := validateProject(client, *projectFlag); err != nil {
			return err
		}
		if err := saveLocalConfig(LocalConfig{Project: *projectFlag}); err != nil {
			return fmt.Errorf("write %s: %w", localConfigFile, err)
		}
		fmt.Fprintf(os.Stderr, "Project set to %q — saved to %s\n", *projectFlag, localConfigFile)
		writeOut(map[string]any{"ok": true, "project": *projectFlag, "file": localConfigFile})
		return nil
	}

	// Interactive picker
	m := newInitModel(client)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return err
	}

	final := result.(initModel)
	if final.err != nil {
		return final.err
	}
	if final.chosen == "" {
		return nil
	}

	var lc LocalConfig
	var kind, slug string
	if final.chosenIsGroup {
		lc = LocalConfig{Group: final.chosen}
		kind, slug = "Group", final.chosen
	} else {
		lc = LocalConfig{Project: final.chosen}
		kind, slug = "Project", final.chosen
	}

	if err := saveLocalConfig(lc); err != nil {
		return fmt.Errorf("write %s: %w", localConfigFile, err)
	}

	fmt.Fprintf(os.Stderr, "%s set to %q — saved to %s\n", kind, slug, localConfigFile)
	writeOut(map[string]any{"ok": true, strings.ToLower(kind): slug, "file": localConfigFile})
	return nil
}
