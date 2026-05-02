package main

import (
	"flag"
	"fmt"
	"os"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Init telemetry early (no-ops if config missing or opted out).
	if cfg, err := loadConfig(); err == nil {
		initTelemetry(cfg)
		defer shutdownTelemetry()
	}

	var err error
	switch os.Args[1] {
	case "login":
		err = cmdLogin(os.Args[2:])
	case "issues":
		err = cmdIssues(os.Args[2:])
	case "tui":
		err = cmdTUI(os.Args[2:])
	case "logs":
		err = cmdLogs(os.Args[2:])
	case "issue":
		err = cmdIssue(os.Args[2:])
	case "events":
		err = cmdEvents(os.Args[2:])
	case "resolve":
		err = cmdResolve(os.Args[2:])
	case "reopen":
		err = cmdReopen(os.Args[2:])
	case "mute":
		err = cmdMute(os.Args[2:])
	case "unmute":
		err = cmdUnmute(os.Args[2:])
	case "projects":
		err = cmdProjects(os.Args[2:])
	case "apikeys":
		err = cmdAPIKeys(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("bb %s (built %s)\n", Version, BuildTime)
		return
	case "help", "--help", "-h":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		reportError(err)
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`bb — BugBarn CLI

Usage: bb <command> [flags]

Commands:
  login       Authenticate with a BugBarn instance
  issues      List issues (JSON output)
  tui         Interactive terminal UI for browsing issues
  logs        Fetch or live-tail structured logs
  issue       Get issue detail
  events      List events for an issue
  resolve     Resolve an issue
  reopen      Reopen a resolved issue
  mute        Mute an issue
  unmute      Unmute an issue
  projects    List or create projects
  apikeys     List API keys
  version     Print version

Authentication:
  bb login --url https://bugbarn.example.com --api-key KEY
  bb login --url https://bugbarn.example.com --username USER --password PASS

Examples:
  bb issues                          # list open issues (JSON)
  bb tui                             # interactive issue browser
  bb logs -f                         # live-tail all logs (colored)
  bb logs -f --level warn            # tail warnings and above
  bb logs --project backend --limit 20
  bb issues --status all --query OOM # search all issues
  bb issue issue-000042              # get issue detail
  bb events issue-000042             # list events for issue
  bb resolve issue-000042            # resolve an issue
  bb projects --create "My App"      # create a new project
  bb apikeys                         # list API keys

Config: ~/.config/bugbarn/cli.json (override with BB_CONFIG env var)
  Set "telemetry": false to disable error reporting to BugBarn.
`)
}
