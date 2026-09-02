package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestProjectLaunchScopeBecomesASwitcherEntry(t *testing.T) {
	m := newModel(testClient().withProject("bugbarn-service"), "open")
	if m.projSlug != "bugbarn-service" {
		t.Fatalf("projSlug = %q, want the launch project", m.projSlug)
	}
	if m.base.project != "" {
		t.Fatal("the baseline client must be unscoped so other projects stay reachable")
	}
	if m.client.project != "bugbarn-service" {
		t.Fatalf("client.project = %q, want the launch project", m.client.project)
	}
}

func TestGroupLaunchScopeIsPreserved(t *testing.T) {
	c := testClient()
	c.group = "bugbarn"
	m := newModel(c, "open")
	if m.base.group != "bugbarn" {
		t.Fatal("a --group launch must keep its group as the baseline scope")
	}
	if got := m.baseScopeLabel(); got != "all projects in group bugbarn" {
		t.Fatalf("baseScopeLabel = %q", got)
	}
}

func TestCycleProjectWrapsThroughAllProjectsEntry(t *testing.T) {
	m := newModel(testClient(), "open")
	m.projects = []project{{Slug: "alpha", Name: "Alpha"}, {Slug: "beta", Name: "Beta"}}

	m, _ = m.cycleProject(1)
	if m.projSlug != "alpha" || m.client.project != "alpha" {
		t.Fatalf("first cycle = %q/%q, want alpha", m.projSlug, m.client.project)
	}
	m, _ = m.cycleProject(1)
	if m.projSlug != "beta" {
		t.Fatalf("second cycle = %q, want beta", m.projSlug)
	}
	m, _ = m.cycleProject(1)
	if m.projSlug != "" {
		t.Fatalf("cycling past the last project should return to all projects, got %q", m.projSlug)
	}
	if m.client != m.base {
		t.Fatal("the all-projects scope must restore the baseline client")
	}
	m, _ = m.cycleProject(-1)
	if m.projSlug != "beta" {
		t.Fatalf("cycling backwards from all projects should land on the last, got %q", m.projSlug)
	}
}

// A project switch replaces the client rather than mutating it, so requests
// already in flight keep talking to the scope they were created with.
func TestSelectProjectDoesNotMutateTheInFlightClient(t *testing.T) {
	m := newModel(testClient(), "open")
	before := m.client
	m, _ = m.selectProject("gamma")
	if before.project != "" {
		t.Fatal("selectProject mutated the client an in-flight request is using")
	}
	if m.client == before {
		t.Fatal("selectProject should install a new scoped client")
	}
}

func TestProjectPickerFiltersOnSlugAndName(t *testing.T) {
	m := newModel(testClient(), "open")
	m.projects = []project{
		{Slug: "bugbarn-service", Name: "BugBarn Service"},
		{Slug: "profotograaf", Name: "Pro Fotograaf"},
	}

	m.projQuery = "fotog"
	got := m.filteredProjectOptions()
	if len(got) != 1 || got[0].slug != "profotograaf" {
		t.Fatalf("name match failed: %+v", got)
	}

	m.projQuery = "bugbarn-s"
	got = m.filteredProjectOptions()
	if len(got) != 1 || got[0].slug != "bugbarn-service" {
		t.Fatalf("slug match failed: %+v", got)
	}

	m.projQuery = ""
	if got := m.filteredProjectOptions(); len(got) != 3 || got[0].slug != "" {
		t.Fatalf("unfiltered list should lead with the all-projects entry: %+v", got)
	}
}

func TestProjectPickerTypingAndSelection(t *testing.T) {
	m := newModel(testClient(), "open")
	m.projects = []project{{Slug: "alpha", Name: "Alpha"}, {Slug: "beta", Name: "Beta"}}
	m = m.openProjectPicker()
	if m.view != viewProjects {
		t.Fatal("p should open the picker")
	}

	for _, r := range "beta" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if m.projSlug != "beta" {
		t.Fatalf("typing a filter and pressing enter should select beta, got %q", m.projSlug)
	}
	if m.view != viewList {
		t.Fatal("selecting a project should return to the list")
	}
}

func TestProjectPickerBackspaceAndCancel(t *testing.T) {
	m := newModel(testClient(), "open")
	m.projects = []project{{Slug: "alpha"}}
	m = m.openProjectPicker()
	m.projQuery = "alp"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(model)
	if m.projQuery != "al" {
		t.Fatalf("projQuery = %q, want al", m.projQuery)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.view != viewList || m.projSlug != "" {
		t.Fatal("esc should cancel the picker without changing the scope")
	}
}

func TestPickerIsNoOpWithoutAProjectList(t *testing.T) {
	m := newModel(testClient(), "open")
	if m.openProjectPicker().view != viewList {
		t.Error("the picker must not open before the project list has loaded")
	}
	if next, _ := m.cycleProject(1); next.projSlug != "" {
		t.Error("cycling with no projects loaded must not change the scope")
	}
}

// The project list is a switcher convenience; failing to load it must not
// replace the issue list the user actually asked for with an error screen.
func TestProjectListErrorDoesNotClobberTheIssueError(t *testing.T) {
	m := newModel(testClient(), "open")
	m = m.applyIssues(issuesMsg{issues: []domain.Issue{{ID: "BW-1"}}})
	m = m.applyProjects(projectsMsg{err: errors.New("projects down")})
	if m.err != nil {
		t.Fatalf("err = %v, want nil", m.err)
	}
	if len(m.issues) != 1 {
		t.Fatal("the issue list should be untouched")
	}
}

func TestCycleStatusWalksTheFilterSet(t *testing.T) {
	m := newModel(testClient(), "open")
	for _, want := range []string{"resolved", "muted", "all", "open"} {
		m, _ = m.cycleStatus()
		if m.status != want {
			t.Fatalf("status = %q, want %q", m.status, want)
		}
	}
	if !m.loading {
		t.Error("changing the status filter should trigger a reload")
	}
}

func TestListHeaderNamesScopeAndStatus(t *testing.T) {
	m := newModel(testClient(), "open")
	m.width, m.height = 120, 20
	m.issues = []domain.Issue{{ID: "BW-1"}, {ID: "BW-2"}}

	header := m.listHeader()
	if !strings.Contains(header, "2 issues") || !strings.Contains(header, "all projects") ||
		!strings.Contains(header, "open") {
		t.Fatalf("header = %q", header)
	}

	m, _ = m.selectProject("alpha")
	if !strings.Contains(m.listHeader(), "alpha") {
		t.Fatalf("scoped header = %q", m.listHeader())
	}
}

// With a single project selected the slug is the same on every row, so the
// column is dropped and the width goes to the title instead.
func TestIssueRowDropsRedundantProjectColumn(t *testing.T) {
	m := newModel(testClient(), "open")
	m.width, m.height = 120, 20
	iss := domain.Issue{ID: "BW-1", Title: "kaboom", Status: "unresolved",
		ProjectSlug: "alpha", EventCount: 4, LastSeen: time.Now()}

	if !strings.Contains(m.issueRow(iss, false), "alpha") {
		t.Error("unscoped list should show which project an issue belongs to")
	}
	m, _ = m.selectProject("alpha")
	if strings.Contains(m.issueRow(iss, false), "alpha") {
		t.Error("scoped list should not repeat the project on every row")
	}
}
