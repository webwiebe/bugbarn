package main

import (
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestParseInterspersed(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantID    string
		wantPrint bool
		wantProj  string
	}{
		{"trailing flag", []string{"BS2-103", "--print"}, "BS2-103", true, ""},
		{"leading flag", []string{"--print", "BS2-103"}, "BS2-103", true, ""},
		{"flags both sides", []string{"--print", "BS2-103", "--project", "p"}, "BS2-103", true, "p"},
		{"no flags", []string{"BS2-103"}, "BS2-103", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("vibe", flag.ContinueOnError)
			printOnly := fs.Bool("print", false, "")
			project := fs.String("project", "", "")
			pos, err := parseInterspersed(fs, tc.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(pos) == 0 || pos[0] != tc.wantID {
				t.Errorf("id = %v, want %q", pos, tc.wantID)
			}
			if *printOnly != tc.wantPrint {
				t.Errorf("print = %v, want %v", *printOnly, tc.wantPrint)
			}
			if *project != tc.wantProj {
				t.Errorf("project = %q, want %q", *project, tc.wantProj)
			}
		})
	}
}

func sampleIssue() domain.Issue {
	return domain.Issue{
		ID:            "issue-000042",
		Title:         "nil pointer dereference in worker",
		Status:        "unresolved",
		ExceptionType: "runtime.Error",
		ProjectSlug:   "bugbarn-service",
		EventCount:    7,
		FirstSeen:     time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		LastSeen:      time.Date(2026, 7, 6, 12, 30, 0, 0, time.UTC),
	}
}

func sampleEvent() *domain.Event {
	return &domain.Event{
		Payload: event.Event{
			Exception: event.Exception{
				Type:    "TypeError",
				Message: "cannot read property 'id' of undefined",
				Stacktrace: []event.StackFrame{
					{Function: "handle", File: "worker.go", Line: 88},
				},
			},
		},
	}
}

func TestBuildVibePromptIncludesContext(t *testing.T) {
	prompt := buildVibePrompt(sampleIssue(), sampleEvent(), "https://bb.example/app/#/issues/issue-000042")

	for _, want := range []string{
		"issue-000042",
		"nil pointer dereference in worker",
		"https://bb.example/app/#/issues/issue-000042",
		"TypeError: cannot read property 'id' of undefined",
		"at handle (worker.go:88)",
		"bb events issue-000042",
		"senior software engineer",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, prompt)
		}
	}
}

func TestBuildVibePromptSymbolicationPrecedence(t *testing.T) {
	ev := &domain.Event{
		Payload: event.Event{
			Exception: event.Exception{
				Type: "Error",
				Stacktrace: []event.StackFrame{{
					Function:         "n",
					File:             "app.min.js",
					Line:             1,
					OriginalFunction: "renderIssue",
					OriginalFile:     "src/issue.ts",
					OriginalLine:     42,
				}},
			},
		},
	}
	prompt := buildVibePrompt(sampleIssue(), ev, "url")

	if !strings.Contains(prompt, "at renderIssue (src/issue.ts:42)") {
		t.Errorf("expected symbolicated frame, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "app.min.js") {
		t.Errorf("minified location should be replaced by symbolicated one:\n%s", prompt)
	}
}

func TestBuildVibePromptNoEvents(t *testing.T) {
	prompt := buildVibePrompt(sampleIssue(), nil, "url")

	if strings.Contains(prompt, "Most recent occurrence") {
		t.Errorf("occurrence block should be omitted when there are no events:\n%s", prompt)
	}
	// The rest of the prompt still renders.
	if !strings.Contains(prompt, "issue-000042") || !strings.Contains(prompt, "How to work") {
		t.Errorf("prompt should still build without an event:\n%s", prompt)
	}
}

func TestWorktreeName(t *testing.T) {
	cases := map[string]string{
		"issue-000042": "vibe-issue-000042",
		"ISSUE-7":      "vibe-issue-7",
		"a/b c":        "vibe-a-b-c",
	}
	for in, want := range cases {
		if got := worktreeName(in); got != want {
			t.Errorf("worktreeName(%q) = %q, want %q", in, got, want)
		}
	}
}
