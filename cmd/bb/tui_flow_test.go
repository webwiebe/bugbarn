package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// settle feeds msg to the model and then runs every command it returns, feeding
// the resulting messages back in, until no command is left. Quit and the
// terminal hand-off (ExecProcess) end a chain without running anything.
func settle(t *testing.T, m tea.Model, msg tea.Msg) tea.Model {
	t.Helper()
	next, cmd := m.Update(msg)
	return runCmds(t, next, cmd, 0)
}

func runCmds(t *testing.T, m tea.Model, cmd tea.Cmd, depth int) tea.Model {
	t.Helper()
	if cmd == nil || depth > 8 {
		return m
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil, tea.QuitMsg:
		return m
	case tea.BatchMsg:
		for _, c := range msg {
			m = runCmds(t, m, c, depth+1)
		}
		return m
	}
	next, nextCmd := m.Update(msg)
	return runCmds(t, next, nextCmd, depth+1)
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func tuiAgainstAPI(t *testing.T) (*harness, tea.Model) {
	t.Helper()
	h := newHarness(t)
	inEmptyDir(t)
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = newModel(c, "open")
	m = settle(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = runCmds(t, m, m.Init(), 0)
	return h, m
}

func TestTUIListDetailAndResolveFlow(t *testing.T) {
	h, m := tuiAgainstAPI(t)
	h.seedIssue(t, "alpha failure")
	mm := m.(model)
	if !strings.Contains(mm.View(), "No open issues") {
		t.Errorf("empty list view:\n%s", mm.View())
	}

	// Refresh picks the seeded issue up.
	m = settle(t, m, key("R"))
	if got := m.(model); len(got.issues) != 1 || !strings.Contains(got.View(), "alpha failure") {
		t.Fatalf("list after refresh:\n%s", got.View())
	}

	// Enter loads the detail and its events.
	m = settle(t, m, key("enter"))
	d := m.(model)
	if d.view != viewDetail || len(d.events) != 1 || !strings.Contains(d.View(), "alpha failure") {
		t.Fatalf("detail view = %v events=%d:\n%s", d.view, len(d.events), d.View())
	}
	if !strings.Contains(d.detailFooter(), "esc") {
		t.Errorf("detail footer = %q", d.detailFooter())
	}

	// r resolves, which returns to the list with the issue gone from "open".
	m = settle(t, m, key("r"))
	if got := m.(model); got.view != viewList || len(got.issues) != 0 {
		t.Fatalf("after resolve: view=%v issues=%d err=%v", got.view, len(got.issues), got.err)
	}

	// s cycles to resolved, where the issue shows; r there reopens it.
	m = settle(t, m, key("s"))
	if got := m.(model); got.status != "resolved" || len(got.issues) != 1 {
		t.Fatalf("resolved filter: status=%s issues=%d", got.status, len(got.issues))
	}
	m = settle(t, m, key("r"))
	if got := m.(model); len(got.issues) != 0 || got.err != nil {
		t.Fatalf("after reopen: issues=%d err=%v", len(got.issues), got.err)
	}

	// Esc in the detail view returns to the list.
	m = settle(t, m, key("enter"))
	if m.(model).view == viewDetail {
		m = settle(t, m, key("esc"))
	}
	if m.(model).view != viewList {
		t.Error("esc must leave the detail view")
	}
}

func TestTUIProjectSwitcher(t *testing.T) {
	h, m := tuiAgainstAPI(t)
	h.seedIssue(t, "alpha failure")
	if _, err := h.Store.CreateProject(t.Context(), "Second", "second"); err != nil {
		t.Fatal(err)
	}
	m = settle(t, m, key("R"))
	m = runCmds(t, m, m.(model).fetchProjects(), 0)
	if got := len(m.(model).projects); got != 2 {
		t.Fatalf("projects = %d, want 2", got)
	}

	m = settle(t, m, key("p"))
	pm := m.(model)
	if pm.view != viewProjects || !strings.Contains(pm.View(), "second") {
		t.Fatalf("picker:\n%s", pm.View())
	}
	m = settle(t, m, key("sec"))
	if got := len(m.(model).filteredProjectOptions()); got != 1 {
		t.Errorf("filtered options = %d, want 1", got)
	}
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = settle(t, m, key("esc"))
	if m.(model).view != viewList {
		t.Error("esc must close the picker")
	}

	// Tab cycles to the first project; its list is empty.
	m = settle(t, m, key("tab"))
	if got := m.(model); got.projSlug == "" || len(got.issues) > 1 {
		t.Errorf("after tab: projSlug=%q issues=%d", got.projSlug, len(got.issues))
	}
	if !strings.Contains(m.(model).View(), m.(model).projSlug) {
		t.Error("the list header must name the scope")
	}
}

func TestTUIShowsFetchErrors(t *testing.T) {
	inEmptyDir(t)
	srv := newScripted(t, 500, "boom")
	useConfig(t, apiKeyConfig(srv.URL))
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = newModel(c, "open")
	m = settle(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View(), "Loading") {
		t.Errorf("loading view:\n%s", m.View())
	}
	m = runCmds(t, m, m.Init(), 0)
	got := m.(model)
	if got.err == nil || !strings.Contains(got.View(), "HTTP 500") {
		t.Errorf("error view:\n%s", got.View())
	}
	if len(got.projects) != 0 {
		t.Error("a failed project list must leave the switcher empty")
	}
	if (model{}).View() != "Loading..." {
		t.Error("a model without a size renders the loading text")
	}
}

func TestTUIVibeHandOff(t *testing.T) {
	h, m := tuiAgainstAPI(t)
	issue := h.seedIssue(t, "alpha failure")
	m = settle(t, m, key("R"))

	dir := t.TempDir()
	t.Setenv("PATH", dir)
	m = settle(t, m, key("v"))
	if got := m.(model); got.err == nil || !strings.Contains(got.err.Error(), "claude CLI not found") {
		t.Fatalf("without claude on PATH: err=%v", got.err)
	}

	fake := filepath.Join(dir, "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd, err := prepareVibeCommand(m.(model).client, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != fake || cmd.Args[1] != "--dangerously-skip-permissions" || cmd.Args[2] != "-w" {
		t.Errorf("command = %v", cmd.Args)
	}
	if !strings.Contains(cmd.Args[len(cmd.Args)-1], "alpha failure") {
		t.Error("the prompt must carry the issue")
	}

	// A non-zero exit from Claude ends the session without a bb error.
	if err := launchClaude(issue.ID, "p", true); err != nil {
		t.Errorf("exit status 3 must not surface: %v", err)
	}
	m = settle(t, m, vibeDoneMsg{err: os.ErrClosed})
	if m.(model).err == nil {
		t.Error("a launch failure must surface")
	}
}

func TestTUIGroupScopedSwitcher(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	if _, err := h.Store.CreateGroup(t.Context(), "Team", "team"); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.AssignProjectToGroup(t.Context(), "default", "team"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.CreateProject(t.Context(), "Outside", "outside"); err != nil {
		t.Fatal(err)
	}
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	c.group = "team"
	ps, err := listSwitchableProjects(c)
	if err != nil || len(ps) != 1 || ps[0].Slug != "default" {
		t.Fatalf("projects = %v err=%v", ps, err)
	}
	c.group = "missing"
	if _, err := listSwitchableProjects(c); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown group: %v", err)
	}
}

func TestInitPickerFlow(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	if _, err := h.Store.CreateGroup(t.Context(), "Team", "team"); err != nil {
		t.Fatal(err)
	}
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = newInitModel(c)
	if !strings.Contains(m.View(), "Loading") {
		t.Error("loading view")
	}
	m = runCmds(t, m, m.Init(), 0)
	view := m.View()
	for _, want := range []string{"GROUPS", "PROJECTS", "team", "default"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker lacks %q:\n%s", want, view)
		}
	}
	m = settle(t, m, key("down"))
	m = settle(t, m, key("up"))
	m = settle(t, m, key("down"))
	m = settle(t, m, key("enter"))
	final := m.(initModel)
	if final.chosen != "default" || final.chosenIsGroup {
		t.Errorf("chosen = %q group=%v, want the project below the group", final.chosen, final.chosenIsGroup)
	}
	if final.View() != "" {
		t.Error("a finished picker renders nothing")
	}

	// Error and empty-instance results quit with a message.
	failed, _ := newInitModel(c).Update(initDataMsg{err: os.ErrNotExist})
	if !strings.Contains(failed.View(), "Error") {
		t.Error("error view")
	}
	empty, _ := newInitModel(c).Update(initDataMsg{})
	if !strings.Contains(empty.View(), "no projects found") {
		t.Error("empty view")
	}
	quit, cmd := newInitModel(c).Update(key("q"))
	if cmd == nil || quit == nil {
		t.Error("q must quit")
	}
}
