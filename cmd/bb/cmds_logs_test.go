package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func (h *harness) seedLogs(t *testing.T, entries ...storage.LogEntry) {
	t.Helper()
	proj, err := h.Store.ProjectBySlug(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		entries[i].ProjectID = proj.ID
		if entries[i].ReceivedAt.IsZero() {
			entries[i].ReceivedAt = time.Date(2026, 1, 2, 3, 4, 5+i, 0, time.UTC)
		}
	}
	if err := h.Store.InsertLogEntries(t.Context(), entries); err != nil {
		t.Fatalf("insert logs: %v", err)
	}
}

func TestLogsFetchAgainstRealAPI(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	h.seedLogs(t,
		storage.LogEntry{Level: "info", LevelNum: 30, Message: "service started"},
		storage.LogEntry{Level: "error", LevelNum: 50, Message: "disk full", Data: map[string]any{"disk": "/dev/sda1"}},
		storage.LogEntry{Level: "warn", LevelNum: 40, Message: "slow query"},
	)

	t.Run("raw json with --no-color", func(t *testing.T) {
		out, err := runCmd(t, cmdLogs, "--no-color")
		if err != nil || !strings.Contains(out, `"logs"`) || !strings.Contains(out, "disk full") {
			t.Fatalf("logs: %v\n%s", err, out)
		}
	})
	t.Run("level filter", func(t *testing.T) {
		out, err := runCmd(t, cmdLogs, "--no-color", "--level", "error")
		if err != nil || !strings.Contains(out, "disk full") || strings.Contains(out, "service started") {
			t.Fatalf("logs: %v\n%s", err, out)
		}
	})
	t.Run("query and limit", func(t *testing.T) {
		out, err := runCmd(t, cmdLogs, "--no-color", "--query", "slow", "--limit", "1")
		if err != nil || !strings.Contains(out, "slow query") || strings.Contains(out, "disk full") {
			t.Fatalf("logs: %v\n%s", err, out)
		}
	})
	t.Run("formatted oldest first", func(t *testing.T) {
		out, err := runCmd(t, cmdLogs)
		if err != nil {
			t.Fatal(err)
		}
		started, full := strings.Index(out, "service started"), strings.Index(out, "disk full")
		if started < 0 || full < 0 || started > full {
			t.Errorf("entries are missing or not oldest first:\n%s", out)
		}
		if !strings.Contains(out, "INFO") || !strings.Contains(out, "ERROR") || !strings.Contains(out, "disk:") || !strings.Contains(out, "/dev/sda1") {
			t.Errorf("level or data not rendered:\n%s", out)
		}
	})
	t.Run("project filter hides the project column", func(t *testing.T) {
		out, err := runCmd(t, cmdLogs, "--project", "default")
		if err != nil || strings.Contains(out, "default") {
			t.Errorf("err=%v; the project name is repeated on every line:\n%s", err, out)
		}
	})
	t.Run("group scope", func(t *testing.T) {
		if _, err := h.Store.CreateGroup(t.Context(), "Team", "team"); err != nil {
			t.Fatal(err)
		}
		if err := h.Store.AssignProjectToGroup(t.Context(), "default", "team"); err != nil {
			t.Fatal(err)
		}
		out, err := runCmd(t, cmdLogs, "--group", "team", "--no-color")
		if err != nil || !strings.Contains(out, "disk full") {
			t.Fatalf("logs: %v\n%s", err, out)
		}
	})
	t.Run("flag error", func(t *testing.T) {
		if _, err := runCmd(t, cmdLogs, "--limit", "x"); err == nil {
			t.Error("a bad --limit must fail")
		}
	})
}

// sseServer streams the given lines, then closes the connection.
func sseServer(t *testing.T, status int, lines ...string) (*httptest.Server, func() http.Header) {
	t.Helper()
	var mu sync.Mutex
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Clone()
		mu.Unlock()
		if status != http.StatusOK {
			http.Error(w, "nope", status)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			_, _ = w.Write([]byte(l + "\n\n"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() http.Header { mu.Lock(); defer mu.Unlock(); return got }
}

func TestLogsFollowStreamsAndFilters(t *testing.T) {
	inEmptyDir(t)
	srv, headers := sseServer(t, http.StatusOK,
		": keepalive",
		`data: {"project_slug":"a","level_num":30,"level":"info","message":"quiet one","received_at":"2026-01-02T03:04:05Z"}`,
		`data: {"project_slug":"a","level_num":50,"level":"error","message":"loud one","received_at":"2026-01-02T03:04:06Z"}`,
		`data: {"project_slug":"b","level_num":50,"level":"error","message":"other project","received_at":"2026-01-02T03:04:07Z"}`,
	)
	useConfig(t, apiKeyConfig(srv.URL))

	out, err := runCmd(t, cmdLogs, "-f", "--level", "error", "--project", "a", "--no-color")
	if err != nil {
		t.Fatalf("follow: %v\n%s", err, out)
	}
	if !strings.Contains(out, "loud one") || strings.Contains(out, "quiet one") || strings.Contains(out, "other project") {
		t.Errorf("filters not applied:\n%s", out)
	}
	h := headers()
	if h.Get("X-BugBarn-API-Key") != "k" || h.Get("X-BugBarn-Project") != "a" {
		t.Errorf("stream request headers = %v", h)
	}

	out, err = runCmd(t, cmdLogs, "--follow")
	if err != nil || !strings.Contains(out, "ERROR") || !strings.Contains(out, "loud one") {
		t.Errorf("colored follow: %v\n%s", err, out)
	}
}

func TestLogsFollowSessionAuthAndHTTPError(t *testing.T) {
	inEmptyDir(t)
	srv, headers := sseServer(t, http.StatusOK)
	useConfig(t, Config{URL: srv.URL, Auth: AuthConfig{Type: "session", SessionToken: "tok"}})
	if _, err := runCmd(t, cmdLogs, "-f"); err != nil {
		t.Fatal(err)
	}
	if c := headers().Get("Cookie"); !strings.Contains(c, "bugbarn_session=tok") {
		t.Errorf("cookie = %q", c)
	}

	bad, _ := sseServer(t, http.StatusUnauthorized)
	useConfig(t, apiKeyConfig(bad.URL))
	if _, err := runCmd(t, cmdLogs, "-f"); err == nil || !strings.Contains(err.Error(), "stream: HTTP 401") {
		t.Errorf("err = %v", err)
	}
}

func TestLogHelpers(t *testing.T) {
	levels := map[string]int{"trace": 10, "DEBUG": 20, "info": 30, "Warn": 40, "error": 50, "fatal": 60, "": 0, "weird": 0}
	for name, want := range levels {
		if got := levelNum(name); got != want {
			t.Errorf("levelNum(%q) = %d, want %d", name, got, want)
		}
	}
	for _, l := range []string{"fatal", "error", "warn", "info", "debug", "trace", "other"} {
		if got := levelStyle(l).Render("x"); !strings.Contains(got, "x") {
			t.Errorf("levelStyle(%q) lost the text", l)
		}
	}
	if got := formatLogTime("not a time"); got != "not a time" {
		t.Errorf("an unparseable time must pass through, got %q", got)
	}
	if got := formatLogTime("2026-01-02T03:04:05Z"); len(got) != len("15:04:05") {
		t.Errorf("formatLogTime = %q", got)
	}
}

func TestPrintLogEntryTruncatesLongData(t *testing.T) {
	out := captureStdout(t, func() {
		printLogEntry(logEntry{Level: "info", ProjectSlug: "a", Message: "m", Data: map[string]any{"k": strings.Repeat("z", 300)}}, "")
	})
	if !strings.Contains(out, "...") || strings.Contains(out, strings.Repeat("z", 121)) {
		t.Errorf("long data not truncated: %q", out)
	}
}
