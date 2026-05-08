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
	Project string `json:"project"`
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
			if json.Unmarshal(data, &lc) == nil && lc.Project != "" {
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

type project struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type projectsMsg struct {
	projects []project
	err      error
}

type initModel struct {
	client   *Client
	projects []project
	cursor   int
	err      error
	loading  bool
	chosen   string
}

func newInitModel(client *Client) initModel {
	return initModel{client: client, loading: true}
}

func (m initModel) Init() tea.Cmd {
	return func() tea.Msg {
		data, err := m.client.get("/api/v1/projects")
		if err != nil {
			return projectsMsg{err: err}
		}
		var resp struct {
			Projects []project `json:"projects"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return projectsMsg{err: err}
		}
		return projectsMsg{projects: resp.Projects}
	}
}

func (m initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case projectsMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.projects = msg.projects
		if len(m.projects) == 0 {
			m.err = fmt.Errorf("no projects found — create one with: bb projects --create \"My App\"")
			return m, tea.Quit
		}
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
			if m.cursor < len(m.projects)-1 {
				m.cursor++
			}
		case "enter":
			m.chosen = m.projects[m.cursor].Slug
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m initModel) View() string {
	if m.loading {
		return "\n  Loading projects...\n"
	}
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n", m.err)
	}
	if m.chosen != "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(" Select a project "))
	b.WriteString("\n\n")

	for i, p := range m.projects {
		line := fmt.Sprintf("%-20s  %s", p.Slug, p.Name)
		if i == m.cursor {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(normalStyle.Render(
				lipgloss.NewStyle().Foreground(subtleColor).Render(line),
			))
		}
		b.WriteString("\n")
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	chosen := *projectFlag

	if chosen == "" {
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
		chosen = final.chosen
	}

	if err := saveLocalConfig(LocalConfig{Project: chosen}); err != nil {
		return fmt.Errorf("write %s: %w", localConfigFile, err)
	}

	fmt.Fprintf(os.Stderr, "Project set to %q — saved to %s\n", chosen, localConfigFile)
	writeOut(map[string]any{"ok": true, "project": chosen, "file": localConfigFile})
	return nil
}
