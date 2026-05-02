package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "stream live logs (SSE)")
	fs.BoolVar(follow, "f", false, "stream live logs (SSE)")
	level := fs.String("level", "", "minimum level: trace|debug|info|warn|error|fatal")
	project := fs.String("project", "", "project slug")
	query := fs.String("query", "", "search text")
	limit := fs.Int("limit", 50, "max entries (non-follow mode)")
	noColor := fs.Bool("no-color", false, "disable colored output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	if *follow {
		return streamLogs(client, *level, *project, *noColor)
	}
	return fetchLogs(client, *level, *project, *query, *limit, *noColor)
}

func fetchLogs(client *Client, level, project, query string, limit int, noColor bool) error {
	params := url.Values{}
	if level != "" {
		params.Set("level", level)
	}
	if query != "" {
		params.Set("q", query)
	}
	params.Set("limit", fmt.Sprintf("%d", limit))

	path := "/api/v1/logs?" + params.Encode()
	data, err := client.get(path)
	if err != nil {
		return err
	}

	if noColor {
		return writeRaw(data)
	}

	var resp struct {
		Logs []logEntry `json:"logs"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return writeRaw(data)
	}

	for i := len(resp.Logs) - 1; i >= 0; i-- {
		printLogEntry(resp.Logs[i], project)
	}
	return nil
}

func streamLogs(client *Client, level, project string, noColor bool) error {
	path := "/api/v1/logs/stream"

	req, err := http.NewRequest("GET", client.base+path, nil)
	if err != nil {
		return err
	}

	switch client.config.Auth.Type {
	case "apikey":
		req.Header.Set("X-BugBarn-API-Key", client.config.Auth.APIKey)
	case "session":
		req.AddCookie(&http.Cookie{Name: "bugbarn_session", Value: client.config.Auth.SessionToken})
	}

	resp, err := client.http.Do(req)
	if err != nil {
		return fmt.Errorf("stream connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("stream: HTTP %d", resp.StatusCode)
	}

	levelMin := levelNum(level)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	scanner := bufio.NewScanner(resp.Body)
	for {
		select {
		case <-sigCh:
			return nil
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("stream read: %w", err)
			}
			return nil
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		raw := line[6:]
		var entry logEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			continue
		}

		if levelMin > 0 && entry.LevelNum < levelMin {
			continue
		}
		if project != "" && entry.ProjectSlug != project {
			continue
		}

		if noColor {
			fmt.Println(raw)
		} else {
			printLogEntry(entry, "")
		}
	}
}

type logEntry struct {
	ID          int64          `json:"id"`
	ProjectSlug string         `json:"project_slug"`
	ReceivedAt  string         `json:"received_at"`
	LevelNum    int            `json:"level_num"`
	Level       string         `json:"level"`
	Message     string         `json:"message"`
	Data        map[string]any `json:"data,omitempty"`
}

func printLogEntry(e logEntry, filterProject string) {
	ts := formatLogTime(e.ReceivedAt)

	lvl := levelStyle(e.Level).Width(5).Render(strings.ToUpper(e.Level))
	proj := projectStyle.Render(e.ProjectSlug)
	msg := e.Message

	parts := []string{timeStyle.Render(ts), lvl}
	if filterProject == "" || filterProject != e.ProjectSlug {
		parts = append(parts, proj)
	}
	parts = append(parts, msg)

	fmt.Println(strings.Join(parts, " "))

	if len(e.Data) > 0 {
		for k, v := range e.Data {
			val := fmt.Sprintf("%v", v)
			if len(val) > 120 {
				val = val[:117] + "..."
			}
			fmt.Printf("  %s %s\n",
				lipgloss.NewStyle().Foreground(accentColor).Render(k+":"),
				lipgloss.NewStyle().Foreground(subtleColor).Render(val))
		}
	}
}

func formatLogTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("15:04:05")
}

func levelStyle(level string) lipgloss.Style {
	switch strings.ToLower(level) {
	case "fatal":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(errorColor).Bold(true)
	case "error":
		return lipgloss.NewStyle().Foreground(errorColor).Bold(true)
	case "warn":
		return lipgloss.NewStyle().Foreground(warningColor).Bold(true)
	case "info":
		return lipgloss.NewStyle().Foreground(successColor)
	case "debug":
		return lipgloss.NewStyle().Foreground(subtleColor)
	case "trace":
		return lipgloss.NewStyle().Foreground(dimColor)
	default:
		return lipgloss.NewStyle()
	}
}

func levelNum(name string) int {
	switch strings.ToLower(name) {
	case "trace":
		return 10
	case "debug":
		return 20
	case "info":
		return 30
	case "warn":
		return 40
	case "error":
		return 50
	case "fatal":
		return 60
	default:
		return 0
	}
}
