package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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

	if *apiKey != "" {
		cfg.Auth = AuthConfig{Type: "apikey", APIKey: *apiKey}
		// Verify the key works.
		client := &Client{
			base:   cfg.URL,
			http:   &http.Client{Timeout: 10 * time.Second},
			config: cfg,
		}
		if _, err := client.get("/api/v1/issues?status=open"); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
	} else if *username != "" {
		if *password == "" {
			fmt.Fprint(os.Stderr, "Password: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			*password = string(pw)
		}
		session, csrf, err := loginWithPassword(*urlFlag, *username, *password)
		if err != nil {
			return err
		}
		cfg.Auth = AuthConfig{
			Type:         "session",
			SessionToken: session,
			CSRFToken:    csrf,
		}
	} else {
		return fmt.Errorf("provide --api-key or --username to authenticate")
	}

	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Logged in to %s\n", cfg.URL)
	writeOut(map[string]any{"ok": true, "url": cfg.URL, "auth_type": cfg.Auth.Type})
	return nil
}

func loginWithPassword(baseURL, username, password string) (session, csrf string, err error) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(baseURL+"/api/v1/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("login failed: HTTP %d", resp.StatusCode)
	}

	for _, c := range resp.Cookies() {
		switch c.Name {
		case "bugbarn_session":
			session = c.Value
		case "bugbarn_csrf":
			csrf = c.Value
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("status", *status)
	params.Set("sort", *sort)
	if *query != "" {
		params.Set("q", *query)
	}
	if *project != "" {
		params.Set("project_slug", *project)
	} else if client.config.Project != "" {
		params.Set("project_slug", client.config.Project)
	}

	data, err := client.get("/api/v1/issues?" + params.Encode())
	if err != nil {
		return err
	}
	return writeRaw(data)
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
	if len(args) < 1 {
		return fmt.Errorf("usage: bb resolve <issue-id>")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	data, err := client.post("/api/v1/issues/"+args[0]+"/resolve", nil)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdReopen(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: bb reopen <issue-id>")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	data, err := client.post("/api/v1/issues/"+args[0]+"/reopen", nil)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdMute(args []string) error {
	fs := flag.NewFlagSet("mute", flag.ContinueOnError)
	mode := fs.String("mode", "until_regression", "until_regression|forever")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: bb mute <issue-id> [--mode until_regression|forever]")
	}

	client, err := newClient()
	if err != nil {
		return err
	}
	data, err := client.patch("/api/v1/issues/"+remaining[0]+"/mute", map[string]string{"mute_mode": *mode})
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdUnmute(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: bb unmute <issue-id>")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	data, err := client.patch("/api/v1/issues/"+args[0]+"/unmute", nil)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdProjects(args []string) error {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	create := fs.String("create", "", "create a new project with this name")
	slug := fs.String("slug", "", "project slug (defaults to slugified name)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	if *create != "" {
		body := map[string]string{"name": *create}
		if *slug != "" {
			body["slug"] = *slug
		}
		data, err := client.post("/api/v1/projects", body)
		if err != nil {
			return err
		}
		return writeRaw(data)
	}

	data, err := client.get("/api/v1/projects")
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdAPIKeys(args []string) error {
	client, err := newClient()
	if err != nil {
		return err
	}
	data, err := client.get("/api/v1/apikeys")
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func writeOut(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func writeRaw(data json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		os.Stdout.Write(data)
		fmt.Println()
		return nil
	}
	buf.WriteByte('\n')
	_, err := buf.WriteTo(os.Stdout)
	return err
}
