package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
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
		session, csrf, err := loginWithPassword(context.Background(), *urlFlag, *username, *password)
		if err != nil {
			return err
		}
		cfg.Auth = AuthConfig{
			Type:         "session",
			Username:     *username,
			Password:     *password,
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

func loginWithPassword(ctx context.Context, baseURL, username, password string) (session, csrf string, err error) {
	ctx, span := tracing.Tracer().Start(ctx, "cli.Request",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.method", http.MethodPost),
			attribute.String("http.target", "/api/v1/login"),
		),
	)
	defer span.End()

	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/login", bytes.NewReader(body))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return "", "", fmt.Errorf("login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return "", "", fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	if resp.StatusCode != 200 {
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
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
	project := fs.String("project", "", "project slug filter")
	group := fs.String("group", "", "group slug filter (shows all projects in the group)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}
	if g := resolveGroup(*group); g != "" && *project == "" {
		client.group = g
	} else if p := resolveProject(*project, client); p != "" {
		client.project = p
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	if *group != "" && *project != "" {
		return fmt.Errorf("--group and --project are mutually exclusive")
	}

	// Resolution order: explicit flag > .bugbarn.json group > .bugbarn.json project / global config.
	resolvedGroup := resolveGroup(*group)
	if resolvedGroup != "" && *project == "" {
		if err := validateGroup(client, resolvedGroup); err != nil {
			return err
		}
		client.group = resolvedGroup
	} else {
		projectSlug := resolveProject(*project, client)
		if projectSlug != "" {
			if err := validateProject(client, projectSlug); err != nil {
				return err
			}
			client.project = projectSlug
		}
	}

	params := url.Values{}
	params.Set("status", *status)
	params.Set("sort", *sort)
	if *query != "" {
		params.Set("q", *query)
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
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return fmt.Errorf("usage: bb resolve <issue-id> [--project SLUG]")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if p := resolveProject(*project, client); p != "" {
		client.project = p
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
	client, err := newClient()
	if err != nil {
		return err
	}
	if p := resolveProject(*project, client); p != "" {
		client.project = p
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

	client, err := newClient()
	if err != nil {
		return err
	}
	if p := resolveProject(*project, client); p != "" {
		client.project = p
	}
	data, err := client.patch("/api/v1/issues/"+remaining[0]+"/mute", map[string]string{"mute_mode": *mode})
	if err != nil {
		return err
	}
	return writeRaw(data)
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
	client, err := newClient()
	if err != nil {
		return err
	}
	if p := resolveProject(*project, client); p != "" {
		client.project = p
	}
	data, err := client.patch("/api/v1/issues/"+remaining[0]+"/unmute", nil)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func resolveProject(flag string, c *Client) string {
	if flag != "" {
		return flag
	}
	if lc, ok := findLocalConfig(); ok && lc.Project != "" {
		return lc.Project
	}
	return c.config.Project
}

func resolveGroup(flag string) string {
	if flag != "" {
		return flag
	}
	if lc, ok := findLocalConfig(); ok {
		return lc.Group
	}
	return ""
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
