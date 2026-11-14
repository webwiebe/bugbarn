package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// issueList decodes the JSON that `bb issues` prints.
type issueList struct {
	Issues []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Status  string `json:"status"`
		Project string `json:"project_slug"`
	} `json:"issues"`
	HasMore bool `json:"hasMore"`
}

func decodeIssues(t *testing.T, out string) issueList {
	t.Helper()
	// stderr hints share the capture; the JSON starts at the first brace.
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("no JSON in output: %q", out)
	}
	var l issueList
	dec := json.NewDecoder(strings.NewReader(out[i:]))
	if err := dec.Decode(&l); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return l
}

func TestIssuesListsOpenIssuesAsIndentedJSON(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	h.seedIssue(t, "alpha failure")
	h.seedIssue(t, "beta failure")

	out, err := runCmd(t, cmdIssues)
	if err != nil {
		t.Fatalf("issues: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "{\n  \"") {
		t.Errorf("output is not indented JSON: %q", out[:min(len(out), 40)])
	}
	if got := len(decodeIssues(t, out).Issues); got != 2 {
		t.Errorf("issues = %d, want 2", got)
	}
}

func TestIssuesFiltersAndPaging(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	a := h.seedIssue(t, "alpha failure")
	h.seedIssue(t, "beta failure")
	h.seedIssue(t, "gamma failure")
	if _, err := runCmd(t, cmdResolve, a.ID); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		args    []string
		want    int
		hasMore bool
		hint    string
	}{
		{"default is open only", nil, 2, false, ""},
		{"resolved", []string{"--status", "resolved"}, 1, false, ""},
		{"all", []string{"--status", "all"}, 3, false, ""},
		{"query", []string{"--status", "all", "--query", "beta"}, 1, false, ""},
		{"sort", []string{"--sort", "first_seen"}, 2, false, ""},
		{"limit reports more", []string{"--status", "all", "--limit", "2"}, 2, true, "more results: use --offset 2 or --all"},
		{"offset skips", []string{"--status", "all", "--limit", "2", "--offset", "2"}, 1, false, ""},
		{"all pages merged", []string{"--status", "all", "--limit", "2", "--all"}, 3, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCmd(t, cmdIssues, tc.args...)
			if err != nil {
				t.Fatalf("issues %v: %v\n%s", tc.args, err, out)
			}
			l := decodeIssues(t, out)
			if len(l.Issues) != tc.want || l.HasMore != tc.hasMore {
				t.Errorf("got %d issues hasMore=%v, want %d hasMore=%v", len(l.Issues), l.HasMore, tc.want, tc.hasMore)
			}
			if tc.hint != "" && !strings.Contains(out, tc.hint) {
				t.Errorf("missing hint %q in %q", tc.hint, out)
			}
		})
	}
}

func TestIssuesFlagAndScopeErrors(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	h.seedIssue(t, "alpha failure")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"limit too high", []string{"--limit", "501"}, "--limit must be between 1 and 500"},
		{"limit negative", []string{"--limit", "-1"}, "--limit must be between"},
		{"offset negative", []string{"--offset", "-1"}, "--offset must be 0 or higher"},
		{"unknown flag", []string{"--bogus"}, "flag provided but not defined"},
		{"bad int", []string{"--limit", "many"}, "invalid value"},
		{"group and project", []string{"--group", "g", "--project", "p"}, "mutually exclusive"},
		{"unknown project", []string{"--project", "nope"}, `project "nope" not found`},
		{"unknown group", []string{"--group", "nope"}, `group "nope" not found`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCmd(t, cmdIssues, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q (output %q)", err, tc.want, out)
			}
		})
	}
}

func TestIssuesScopedToProjectAndGroup(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	h.seedIssue(t, "alpha failure")
	if _, err := h.Store.CreateGroup(t.Context(), "Team", "team"); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.AssignProjectToGroup(t.Context(), "default", "team"); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"--project", "default"}, {"--group", "team"}} {
		out, err := runCmd(t, cmdIssues, args...)
		if err != nil {
			t.Fatalf("issues %v: %v\n%s", args, err, out)
		}
		if got := len(decodeIssues(t, out).Issues); got != 1 {
			t.Errorf("issues %v returned %d, want 1", args, got)
		}
	}
}

func TestIssuesTakesProjectFromLocalConfig(t *testing.T) {
	h := newHarness(t)
	dir := inEmptyDir(t)
	h.seedIssue(t, "alpha failure")

	writeLocal := func(content string) {
		t.Helper()
		if err := writeFile(dir, localConfigFile, content); err != nil {
			t.Fatal(err)
		}
	}
	writeLocal(`{"project":"missing"}`)
	if _, err := runCmd(t, cmdIssues); err == nil || !strings.Contains(err.Error(), `project "missing" not found`) {
		t.Errorf("local project was not applied: %v", err)
	}
	writeLocal(`{"group":"missing"}`)
	if _, err := runCmd(t, cmdIssues); err == nil || !strings.Contains(err.Error(), `group "missing" not found`) {
		t.Errorf("local group was not applied: %v", err)
	}
	writeLocal(`{"project":"default"}`)
	if _, err := runCmd(t, cmdIssues); err != nil {
		t.Errorf("local project default: %v", err)
	}
}

func TestIssueEventsAndMutations(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	issue := h.seedIssue(t, "alpha failure")

	out, err := runCmd(t, cmdIssue, issue.ID)
	if err != nil || !strings.Contains(out, "alpha failure") {
		t.Fatalf("issue: %v\n%s", err, out)
	}
	out, err = runCmd(t, cmdEvents, "--limit", "1", issue.ID)
	if err != nil || !strings.Contains(out, `"events"`) {
		t.Fatalf("events: %v\n%s", err, out)
	}

	steps := []struct {
		name string
		cmd  func([]string) error
		args []string
		want string
	}{
		{"mute", cmdMute, []string{"--mode", "forever", issue.ID}, `"status": "muted"`},
		{"unmute", cmdUnmute, []string{issue.ID}, `"status": "unresolved"`},
		{"resolve", cmdResolve, []string{"--project", "default", issue.ID}, `"status": "resolved"`},
		{"reopen", cmdReopen, []string{issue.ID}, `"status": "unresolved"`},
	}
	for _, s := range steps {
		out, err := runCmd(t, s.cmd, s.args...)
		if err != nil || !strings.Contains(out, s.want) {
			t.Fatalf("%s: err=%v, want %s in\n%s", s.name, err, s.want, out)
		}
	}
}

func TestIssueCommandUsageErrors(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	_ = h
	cases := []struct {
		name string
		cmd  func([]string) error
		args []string
		want string
	}{
		{"issue", cmdIssue, nil, "usage: bb issue"},
		{"events", cmdEvents, nil, "usage: bb events"},
		{"events flag first", cmdEvents, []string{"--limit", "5"}, "usage: bb events"},
		{"resolve", cmdResolve, nil, "usage: bb resolve"},
		{"reopen", cmdReopen, nil, "usage: bb reopen"},
		{"mute", cmdMute, nil, "usage: bb mute"},
		{"unmute", cmdUnmute, nil, "usage: bb unmute"},
		{"resolve bad flag", cmdResolve, []string{"--nope", "x"}, "not defined"},
		{"mute bad flag", cmdMute, []string{"--nope", "x"}, "not defined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runCmd(t, tc.cmd, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestMissingIssueMapsTo404(t *testing.T) {
	newHarness(t)
	inEmptyDir(t)
	for name, cmd := range map[string]func([]string) error{
		"issue": cmdIssue, "events": cmdEvents, "resolve": cmdResolve, "mute": cmdMute,
	} {
		_, err := runCmd(t, cmd, "ZZZ-999")
		if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("%s: err = %v, want HTTP 404", name, err)
		}
	}
}

func TestVibePrintBuildsPromptFromIssue(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	issue := h.seedIssue(t, "alpha failure")

	out, err := runCmd(t, cmdVibe, issue.ID, "--print")
	if err != nil {
		t.Fatalf("vibe: %v\n%s", err, out)
	}
	for _, want := range []string{"alpha failure", "/app/#/issues/" + issue.ID, "bb resolve " + issue.ID} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt lacks %q:\n%s", want, out)
		}
	}
	if _, err := runCmd(t, cmdVibe); err == nil || !strings.Contains(err.Error(), "usage: bb vibe") {
		t.Errorf("no id: %v", err)
	}
	if _, err := runCmd(t, cmdVibe, "ZZZ-999", "--print"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("missing issue: %v", err)
	}
}
