package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	urlFlag := fs.String("url", os.Getenv("BUGBARN_URL"), "BugBarn instance URL")
	apiKey := fs.String("api-key", os.Getenv("BUGBARN_API_KEY"), "API key (full scope)")
	username := fs.String("username", os.Getenv("BUGBARN_USERNAME"), "username")
	password := fs.String("password", "", "password (omit to prompt)")
	project := fs.String("project", "", "default project slug")
	noTelemetry := fs.Bool("no-telemetry", false, "disable error reporting to BugBarn")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *urlFlag == "" {
		return fmt.Errorf("--url is required (or set BUGBARN_URL)")
	}
	*urlFlag = strings.TrimRight(*urlFlag, "/")

	cfg := Config{
		URL:     *urlFlag,
		Project: *project,
	}
	if *noTelemetry {
		f := false
		cfg.Telemetry = &f
	}

	if err := authenticate(&cfg, *apiKey, *username, *password); err != nil {
		return err
	}

	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Logged in to %s\n", cfg.URL)
	writeOut(map[string]any{"ok": true, "url": cfg.URL, "auth_type": cfg.Auth.Type})
	return nil
}

// authenticate fills cfg.Auth from an API key, which it verifies against the
// server, or from a username and password, prompting for the password when it
// is empty.
func authenticate(cfg *Config, apiKey, username, password string) error {
	switch {
	case apiKey != "":
		cfg.Auth = AuthConfig{Type: "apikey", APIKey: apiKey}
		// Verify the key works.
		client := &Client{
			base:   cfg.URL,
			http:   &http.Client{Timeout: 10 * time.Second},
			config: *cfg,
		}
		if _, err := client.get("/api/v1/issues?status=open"); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
	case username != "":
		if password == "" {
			fmt.Fprint(os.Stderr, "Password: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			password = string(pw)
		}
		session, csrf, err := loginWithPassword(context.Background(), cfg.URL, username, password)
		if err != nil {
			return err
		}
		cfg.Auth = AuthConfig{
			Type:         "session",
			Username:     username,
			Password:     password,
			SessionToken: session,
			CSRFToken:    csrf,
		}
	default:
		return fmt.Errorf("provide --api-key or --username to authenticate")
	}
	return nil
}

func loginWithPassword(ctx context.Context, baseURL, username, password string) (session, csrf string, err error) {
	c := &Client{base: baseURL, http: http.DefaultClient}
	resp, err := c.roundTrip(ctx, http.MethodPost, "/api/v1/login", map[string]string{"username": username, "password": password})
	if err != nil {
		return "", "", fmt.Errorf("login request: %w", err)
	}
	if resp.status != http.StatusOK {
		return "", "", fmt.Errorf("login failed: HTTP %d", resp.status)
	}
	for _, ck := range resp.cookies {
		switch ck.Name {
		case "bugbarn_session":
			session = ck.Value
		case "bugbarn_csrf":
			csrf = ck.Value
		}
	}
	if session == "" {
		return "", "", fmt.Errorf("no session cookie in response")
	}
	return session, csrf, nil
}

func cmdTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	status := fs.String("status", "open", "open|resolved|muted|all")
	project := fs.String("project", "", "project slug filter")
	group := fs.String("group", "", "group slug filter (shows all projects in the group)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}
	if err := scopeClient(client, *project, *group); err != nil {
		return err
	}
	return runTUI(client, *status)
}

func cmdIssues(args []string) error {
	fs := flag.NewFlagSet("issues", flag.ContinueOnError)
	status := fs.String("status", "open", "open|resolved|muted|all")
	sort := fs.String("sort", "last_seen", "last_seen|first_seen|event_count")
	query := fs.String("query", "", "search text")
	project := fs.String("project", "", "project slug filter")
	group := fs.String("group", "", "group slug filter (shows all projects in the group)")
	limit := fs.Int("limit", 0, "page size, 1 to 500 (default: the server's 100)")
	offset := fs.Int("offset", 0, "skip this many issues")
	all := fs.Bool("all", false, "follow every page and print one merged list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paging := issuePaging{limit: *limit, offset: *offset, all: *all, offsetSet: flagWasSet(fs, "offset")}
	if err := paging.validate(); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}
	if err := scopeClient(client, *project, *group); err != nil {
		return err
	}

	params := url.Values{}
	params.Set("status", *status)
	params.Set("sort", *sort)
	if *query != "" {
		params.Set("q", *query)
	}

	res, err := fetchIssues(client, params.Encode(), paging)
	if err != nil {
		return err
	}
	if res.hint != "" {
		fmt.Fprintln(os.Stderr, res.hint)
	}
	return writeRaw(res.body)
}

// flagWasSet reports whether the named flag appeared on the command line.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func cmdIssue(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: bb issue <issue-id>")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	data, err := client.get("/api/v1/issues/" + args[0])
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdEvents(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	limit := fs.Int("limit", 25, "max events to return")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: bb events <issue-id> [--limit N]")
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("limit", fmt.Sprintf("%d", *limit))

	data, err := client.get("/api/v1/issues/" + remaining[0] + "/events?" + params.Encode())
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdResolve(args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: bb resolve <issue-id> [--project SLUG]")
	}
	client, err := newProjectClient(*project)
	if err != nil {
		return err
	}
	data, err := client.post("/api/v1/issues/"+remaining[0]+"/resolve", nil)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdReopen(args []string) error {
	fs := flag.NewFlagSet("reopen", flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: bb reopen <issue-id> [--project SLUG]")
	}
	client, err := newProjectClient(*project)
	if err != nil {
		return err
	}
	data, err := client.post("/api/v1/issues/"+remaining[0]+"/reopen", nil)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdMute(args []string) error {
	fs := flag.NewFlagSet("mute", flag.ContinueOnError)
	mode := fs.String("mode", "until_regression", "until_regression|forever")
	project := fs.String("project", "", "project slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: bb mute <issue-id> [--mode until_regression|forever] [--project SLUG]")
	}

	client, err := newProjectClient(*project)
	if err != nil {
		return err
	}
	data, err := client.patch("/api/v1/issues/"+remaining[0]+"/mute", map[string]string{"mute_mode": *mode})
	if err != nil {
		return err
	}
	if err := writeRaw(data); err != nil {
		return err
	}
	return requireIssueStatus(data, remaining[0], "muted")
}

func cmdUnmute(args []string) error {
	fs := flag.NewFlagSet("unmute", flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: bb unmute <issue-id> [--project SLUG]")
	}
	client, err := newProjectClient(*project)
	if err != nil {
		return err
	}
	data, err := client.patch("/api/v1/issues/"+remaining[0]+"/unmute", nil)
	if err != nil {
		return err
	}
	if err := writeRaw(data); err != nil {
		return err
	}
	return requireIssueStatus(data, remaining[0], "unresolved")
}

func writeOut(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeRaw(data json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		_, _ = os.Stdout.Write(data)
		fmt.Println()
		return nil
	}
	buf.WriteByte('\n')
	_, err := buf.WriteTo(os.Stdout)
	return err
}
