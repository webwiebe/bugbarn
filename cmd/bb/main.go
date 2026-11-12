package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

// commands maps each subcommand to its entry point; each takes the arguments
// after the subcommand name.
var commands = map[string]func(args []string) error{
	"init":        cmdInit,
	"login":       cmdLogin,
	"issues":      cmdIssues,
	"tui":         cmdTUI,
	"logs":        cmdLogs,
	"issue":       cmdIssue,
	"events":      cmdEvents,
	"vibe":        cmdVibe,
	"resolve":     cmdResolve,
	"reopen":      cmdReopen,
	"mute":        cmdMute,
	"unmute":      cmdUnmute,
	"projects":    cmdProjects,
	"monitors":    cmdMonitors,
	"groups":      cmdGroups,
	"apikeys":     cmdAPIKeys,
	"key-request": cmdKeyRequest,
}

func main() {
	os.Exit(runCommand(os.Args))
}

// runCommand executes the command named by args[1] and returns the process exit code.
func runCommand(args []string) int {
	if len(args) < 2 {
		printUsage()
		return 1
	}

	switch args[1] {
	case "version", "--version", "-v":
		fmt.Printf("bb %s (built %s)\n", Version, BuildTime)
		return 0
	case "help", "--help", "-h":
		printUsage()
		return 0
	}

	command, ok := commands[args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[1])
		printUsage()
		return 1
	}

	// Init telemetry early (no-ops if config missing or opted out).
	if cfg, err := loadConfig(); err == nil {
		initTelemetry(cfg)
		defer shutdownTelemetry()
	}

	err := command(args[2:])
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	reportError(err)
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return 1
}

func printUsage() {
	fmt.Print(`bb — BugBarn CLI

Usage: bb <command> [flags]

Commands:
  init        Set default project for current directory
  login       Authenticate with a BugBarn instance
  issues      List issues (JSON output)
  tui         Interactive terminal UI for browsing issues
  logs        Fetch or live-tail structured logs
  issue       Get issue detail
  events      List events for an issue
  vibe        Start a Claude Code session for an issue (own worktree)
  resolve     Resolve an issue
  reopen      Reopen a resolved issue
  mute        Mute an issue
  unmute      Unmute an issue
  monitors    List check-in monitors of a project
  projects    List or create projects
  groups      List or create project groups
  apikeys     List API keys
  key-request Ask an admin for an ingest key for an existing project
  version     Print version

Authentication:
  bb login --url https://bugbarn.example.com --api-key KEY
  bb login --url https://bugbarn.example.com --username USER --password PASS

Examples:
  bb init                            # set project for this directory
  bb issues                          # list open issues (JSON)
  bb tui                             # interactive issue browser (p/tab switch project)
  bb logs -f                         # live-tail all logs (colored)
  bb logs -f --level warn            # tail warnings and above
  bb logs --project backend --limit 20
  bb issues --status all --query OOM # search all issues
  bb issues --all                    # every page, not just the first 100
  bb issue issue-000042              # get issue detail
  bb events issue-000042             # list events for issue
  bb vibe issue-000042               # open a Claude Code session for the issue
  bb resolve issue-000042            # resolve an issue
  bb projects --create "My App"      # create a new project
  bb groups                          # list project groups
  bb issues --group bugbarn          # issues across all bugbarn-* projects
  bb apikeys                         # list API keys
  bb key-request geo --label org/repo --output-secret | gh secret set BUGBARN_API_KEY

Config: ~/.config/bugbarn/cli.json (override with BB_CONFIG env var)
  Set "telemetry": false to disable error reporting to BugBarn.
`)
}
