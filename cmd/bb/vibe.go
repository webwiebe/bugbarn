package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// cmdVibe launches an interactive Claude Code session pre-seeded with the
// context of a BugBarn issue. The session runs in its own git worktree with
// permission checks bypassed, so the user can dive straight into fixing.
func cmdVibe(args []string) error {
	fs := flag.NewFlagSet("vibe", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "print the prompt and exit without launching Claude")
	noWorktree := fs.Bool("no-worktree", false, "run Claude in the current directory instead of a new worktree")
	project := fs.String("project", "", "project slug")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: bb vibe <issue-id> [--print] [--no-worktree] [--project SLUG]")
	}
	id := positional[0]

	client, err := newClient()
	if err != nil {
		return err
	}
	if p := resolveProject(*project, client); p != "" {
		client.project = p
	}

	issue, err := fetchIssue(client, id)
	if err != nil {
		return err
	}
	latest, err := fetchLatestEvent(client, id)
	if err != nil {
		return err
	}

	dashboardURL := strings.TrimRight(client.config.URL, "/") + "/app/#/issues/" + id
	prompt := buildVibePrompt(issue, latest, dashboardURL)

	if *printOnly {
		fmt.Println(prompt)
		return nil
	}

	return launchClaude(id, prompt, *noWorktree)
}

// fetchIssue retrieves issue detail from GET /api/v1/issues/{id}.
func fetchIssue(client *Client, id string) (domain.Issue, error) {
	data, err := client.get("/api/v1/issues/" + id)
	if err != nil {
		return domain.Issue{}, err
	}
	var resp struct {
		Issue domain.Issue `json:"issue"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return domain.Issue{}, fmt.Errorf("parse issue response: %w", err)
	}
	return resp.Issue, nil
}

// fetchLatestEvent retrieves the most recent event for an issue, or nil if the
// issue has no retained events.
func fetchLatestEvent(client *Client, id string) (*domain.Event, error) {
	data, err := client.get("/api/v1/issues/" + id + "/events?limit=1")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Events []domain.Event `json:"events"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse events response: %w", err)
	}
	if len(resp.Events) == 0 {
		return nil, nil
	}
	return &resp.Events[0], nil
}

// claudeCommand builds the exec.Cmd that launches Claude Code for an issue.
// --dangerously-skip-permissions is ALWAYS passed: a frictionless, no-approval
// session is the whole point of the vibe workflow. Stdio is left unset so the
// caller (CLI or the TUI's tea.ExecProcess) can wire the terminal.
func claudeCommand(id, prompt string, noWorktree bool) (*exec.Cmd, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude CLI not found on PATH — install Claude Code first")
	}

	claudeArgs := []string{"--dangerously-skip-permissions"}
	if !noWorktree {
		claudeArgs = append(claudeArgs, "-w", worktreeName(id))
	}
	claudeArgs = append(claudeArgs, prompt)

	return exec.Command(path, claudeArgs...), nil
}

// launchClaude runs the Claude Code CLI, wiring the current terminal to the
// child (used by the `bb vibe` command).
func launchClaude(id, prompt string, noWorktree bool) error {
	cmd, err := claudeCommand(id, prompt, noWorktree)
	if err != nil {
		return err
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// A non-zero exit means Claude ran and the session ended (the user quit,
		// or Claude reported its own status) — not a bb failure. Only surface
		// errors from failing to start the process at all.
		if _, ok := err.(*exec.ExitError); ok {
			return nil
		}
		return fmt.Errorf("launch claude: %w", err)
	}
	return nil
}

// prepareVibeCommand fetches an issue and its latest event, builds the seeded
// prompt, and returns a ready-to-run Claude command. Used by the TUI's vibe
// action, where it is handed to tea.ExecProcess.
func prepareVibeCommand(client *Client, id string) (*exec.Cmd, error) {
	issue, err := fetchIssue(client, id)
	if err != nil {
		return nil, err
	}
	latest, err := fetchLatestEvent(client, id)
	if err != nil {
		return nil, err
	}
	dashboardURL := strings.TrimRight(client.config.URL, "/") + "/app/#/issues/" + id
	prompt := buildVibePrompt(issue, latest, dashboardURL)
	return claudeCommand(id, prompt, false)
}

// worktreeName derives a git-worktree-safe name from an issue id, e.g.
// "issue-000042" -> "vibe-issue-000042".
func worktreeName(id string) string {
	var b strings.Builder
	b.WriteString("vibe-")
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// buildVibePrompt assembles the senior-developer prompt seeded into the Claude
// session. ev may be nil when the issue has no retained events.
func buildVibePrompt(issue domain.Issue, ev *domain.Event, dashboardURL string) string {
	var b strings.Builder

	b.WriteString("You are a senior software engineer picking up a live production error from BugBarn, our self-hosted error tracker. ")
	b.WriteString("Work this issue end to end with the rigor of an experienced engineer: reason precisely about the failure, ")
	b.WriteString("find the true root cause (not a surface patch), and ship a minimal, well-tested fix ")
	b.WriteString("that matches the surrounding code. ")
	b.WriteString("Honor the repo's CLAUDE.md rules — especially the error-boundary and logging conventions.\n\n")

	b.WriteString("## The issue\n")
	fmt.Fprintf(&b, "- ID:        %s\n", issue.ID)
	fmt.Fprintf(&b, "- Title:     %s\n", issue.Title)
	fmt.Fprintf(&b, "- Status:    %s\n", issue.Status)
	if issue.ProjectSlug != "" {
		fmt.Fprintf(&b, "- Project:   %s\n", issue.ProjectSlug)
	}
	if issue.ExceptionType != "" {
		fmt.Fprintf(&b, "- Exception: %s\n", issue.ExceptionType)
	}
	fmt.Fprintf(&b, "- Seen:      %d events (first %s, last %s)\n",
		issue.EventCount, formatTime(issue.FirstSeen), formatTime(issue.LastSeen))
	fmt.Fprintf(&b, "- Dashboard: %s\n\n", dashboardURL)

	if ev != nil {
		writeOccurrence(&b, ev)
	}

	b.WriteString("## How to dig deeper\n")
	b.WriteString("The `bb` CLI is available and already authenticated:\n")
	fmt.Fprintf(&b, "- `bb issue %s`   — full issue detail (JSON)\n", issue.ID)
	fmt.Fprintf(&b, "- `bb events %s`  — recent events with stack traces\n", issue.ID)
	fmt.Fprintf(&b, "- `bb resolve %s` — resolve the issue once the fix is confirmed\n\n", issue.ID)

	b.WriteString("## How to work\n")
	b.WriteString("1. Read the stack trace and locate the failing code in this repo.\n")
	b.WriteString("2. Establish the root cause before changing anything — state your hypothesis.\n")
	b.WriteString("3. Implement a minimal, correct fix following existing patterns.\n")
	b.WriteString("4. Add or update a test that would have caught this regression.\n")
	b.WriteString("5. Run `go build ./...` and `go test ./...`. Do not stop on a red build.\n")
	b.WriteString("6. Summarize the root cause and the fix when done.\n\n")

	b.WriteString("Begin by investigating the issue.")

	return b.String()
}

// writeOccurrence renders the most recent event's exception and stack trace.
func writeOccurrence(b *strings.Builder, ev *domain.Event) {
	exc := ev.Payload.Exception
	b.WriteString("## Most recent occurrence\n")
	if exc.Type != "" || exc.Message != "" {
		fmt.Fprintf(b, "%s: %s\n", exc.Type, exc.Message)
	} else if ev.Message != "" {
		fmt.Fprintf(b, "%s\n", ev.Message)
	}
	if len(exc.Stacktrace) > 0 {
		b.WriteString("Stack trace:\n")
		const maxFrames = 30
		for i, f := range exc.Stacktrace {
			if i >= maxFrames {
				fmt.Fprintf(b, "  ... (%d more frames)\n", len(exc.Stacktrace)-maxFrames)
				break
			}
			fn, file, line := frameLocation(f)
			fmt.Fprintf(b, "  at %s (%s:%d)\n", fn, file, line)
		}
	}
	b.WriteString("\n")
}

// frameLocation returns the best available location for a stack frame,
// preferring source-map-symbolicated positions over minified ones.
func frameLocation(f event.StackFrame) (fn, file string, line int) {
	fn, file, line = f.Function, f.File, f.Line
	if f.OriginalFunction != "" {
		fn = f.OriginalFunction
	}
	if f.OriginalFile != "" {
		file = f.OriginalFile
		line = f.OriginalLine
	}
	if fn == "" {
		fn = "?"
	}
	if file == "" {
		file = "?"
	}
	return fn, file, line
}

// parseInterspersed parses a flag set that may have positional arguments mixed
// in among the flags. Go's stdlib flag package stops at the first positional,
// which would silently drop a trailing flag like `bb vibe <id> --print` — a
// dangerous surprise here, since dropping --print means actually launching a
// no-approval Claude session instead of a dry run.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	return positional, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format("2006-01-02 15:04 MST")
}
