package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func testClient() *Client {
	return &Client{base: "https://bugbarn.example.com"}
}

// richEvent is a fully populated occurrence: every section the detail pane can
// render is present, so a test can assert nothing is silently dropped.
func richEvent() domain.Event {
	return domain.Event{
		ID:         "evt-1",
		IssueID:    "BW-3",
		ReceivedAt: time.Now().Add(-2 * time.Minute),
		ObservedAt: time.Now().Add(-3 * time.Minute),
		Severity:   "error",
		Message:    "boom",
		Regressed:  true,
		Payload: event.Event{
			SDKName:  "bugbarn-go/1.2.3",
			TraceID:  "abc123",
			SpanID:   "span9",
			Severity: "error",
			Exception: event.Exception{
				Type:    "runtime.Error",
				Message: "index out of range",
				Stacktrace: []event.StackFrame{
					{Function: "handleRequest", File: "internal/api/server.go", Line: 142, Module: "api"},
					{
						Function: "n", File: "bundle.min.js", Line: 1, Column: 8821,
						OriginalFunction: "renderIssue", OriginalFile: "src/issue.ts", OriginalLine: 44,
						Snippet: "  return issue.title.slice(0, n)",
					},
				},
			},
			User:       event.UserContext{ID: "u-9", Email: "dev@example.com", Username: "dev"},
			Attributes: map[string]any{"http.route": "/api/v1/issues", "retry": float64(3)},
			Resource:   map[string]any{"service.name": "bugbarn-writer"},
			Breadcrumbs: []event.Breadcrumb{
				{Timestamp: "2026-09-02T10:00:00Z", Category: "http", Message: "GET /api/v1/issues", Level: "info",
					Data: map[string]any{"status": float64(200)}},
			},
		},
	}
}

func detailModel(t *testing.T, ev domain.Event) model {
	t.Helper()
	m := newModel(testClient(), "open")
	m.width, m.height = 120, 40
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(model)

	issue := domain.Issue{
		ID: "BW-3", Title: "index out of range", Status: "unresolved",
		ProjectSlug: "bugbarn-service", EventCount: 12,
		FirstSeen: time.Now().Add(-48 * time.Hour), LastSeen: time.Now().Add(-2 * time.Minute),
		Fingerprint: "fp-1", FingerprintExplanation: []string{"grouped by exception type"},
	}
	next, _ = m.Update(detailMsg{detail: issue})
	m = next.(model)
	next, _ = m.Update(eventsMsg{issueID: "BW-3", events: []domain.Event{ev}})
	return next.(model)
}

func TestDetailContentRendersEveryEventSection(t *testing.T) {
	m := detailModel(t, richEvent())
	out := m.renderDetailContent()

	for _, want := range []string{
		"Occurrence 1 of 1",
		"evt-1",
		"bugbarn-go/1.2.3",
		"abc123", "span9",
		"reopened the issue",
		"runtime.Error", "index out of range",
		"Stack Trace (2 frames)",
		"handleRequest", "internal/api/server.go:142 [api]",
		"renderIssue", "src/issue.ts:44",
		"built from bundle.min.js:1:8821",
		"return issue.title.slice(0, n)",
		"dev@example.com",
		"Breadcrumbs (1)",
		"GET /api/v1/issues",
		"status:", "200",
		"Attributes", "http.route:", "retry:", "3",
		"Resource", "service.name:", "bugbarn-writer",
		"Grouping", "grouped by exception type",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail content missing %q\n---\n%s", want, out)
		}
	}
}

// The old detail pane capped the trace at 15 frames with no way to see the
// rest; the viewport scrolls, so every frame must now be rendered.
func TestDetailRendersAllStackFrames(t *testing.T) {
	ev := richEvent()
	frames := make([]event.StackFrame, 30)
	for i := range frames {
		frames[i] = event.StackFrame{Function: "frame" + string(rune('A'+i%26)), File: "f.go", Line: i + 1}
	}
	frames[29] = event.StackFrame{Function: "deepestFrame", File: "deep.go", Line: 99}
	ev.Payload.Exception.Stacktrace = frames

	out := detailModel(t, ev).renderDetailContent()
	if !strings.Contains(out, "Stack Trace (30 frames)") || !strings.Contains(out, "deepestFrame") {
		t.Errorf("expected all 30 frames rendered, got:\n%s", out)
	}
}

func TestCurrentEventFallsBackToRepresentative(t *testing.T) {
	m := newModel(testClient(), "open")
	m.detail = domain.Issue{
		ID: "BW-9", LastSeen: time.Now(),
		RepresentativeEvent: event.Event{
			Message:   "no stored events",
			Exception: event.Exception{Type: "ValueError", Message: "bad input"},
		},
	}
	ev := m.currentEvent()
	if ev.Payload.Exception.Type != "ValueError" {
		t.Fatalf("expected the representative event, got %+v", ev.Payload.Exception)
	}
	if !strings.Contains(m.renderDetailContent(), "showing the issue's representative event") {
		t.Error("expected the fallback to be labeled as such")
	}
}

func TestOccurrenceStepWrapsAround(t *testing.T) {
	m := detailModel(t, richEvent())
	m.events = []domain.Event{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	m = m.stepOccurrence(1)
	if m.eventIdx != 1 {
		t.Fatalf("eventIdx = %d, want 1", m.eventIdx)
	}
	m = m.stepOccurrence(-1)
	m = m.stepOccurrence(-1)
	if m.eventIdx != 2 {
		t.Fatalf("stepping back past the first occurrence should wrap to the last, got %d", m.eventIdx)
	}
	if got := m.occurrencePosition(); got != "3/3" {
		t.Fatalf("occurrencePosition = %q, want 3/3", got)
	}
}

// A slow /events response for an issue the user has already left must not
// overwrite the occurrences of the issue now on screen.
func TestStaleEventsResponseIsIgnored(t *testing.T) {
	m := detailModel(t, richEvent())
	next := m.applyEvents(eventsMsg{issueID: "BW-OTHER", events: []domain.Event{{ID: "wrong"}}})
	if len(next.events) != 1 || next.events[0].ID != "evt-1" {
		t.Fatalf("stale response leaked into the model: %+v", next.events)
	}
}

func TestEventsErrorIsSurfacedNotSwallowed(t *testing.T) {
	m := detailModel(t, richEvent())
	m = m.applyEvents(eventsMsg{issueID: "BW-3", err: errors.New("boom")})
	if !strings.Contains(m.renderDetailContent(), "could not load recent occurrences: boom") {
		t.Error("expected the events error to be reported in the detail pane")
	}
}

func TestToggleResolvePicksTheRightTransition(t *testing.T) {
	m := newModel(testClient(), "open")
	for _, tc := range []struct {
		status string
		want   bool // expect a command
	}{
		{"unresolved", true}, {"regressed", true}, {"resolved", true}, {"muted", true}, {"", false},
	} {
		_, cmd := m.toggleResolve(domain.Issue{ID: "BW-1", Status: tc.status})
		if (cmd != nil) != tc.want {
			t.Errorf("status %q: got cmd=%v, want %v", tc.status, cmd != nil, tc.want)
		}
	}
}

func TestProjectsInGroup(t *testing.T) {
	id := int64(7)
	other := int64(8)
	got := projectsInGroup([]project{
		{Slug: "in", GroupID: &id},
		{Slug: "out", GroupID: &other},
		{Slug: "ungrouped"},
	}, id)
	if len(got) != 1 || got[0].Slug != "in" {
		t.Fatalf("projectsInGroup = %+v", got)
	}
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"héllo wörld", 6, "héllo…"}, // must cut on rune boundaries
		{"hello", 1, "…"},
		{"hello", 0, ""},
	} {
		if got := truncate(tc.in, tc.width); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

func TestFormatValue(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{nil, "null"},
		{"text", "text"},
		{true, "true"},
		{float64(3), "3"}, // JSON numbers must not render as "3.0"
		{1.5, "1.5"},
		{[]any{"a", "b"}, `["a","b"]`},
	} {
		if got := formatValue(tc.in); got != tc.want {
			t.Errorf("formatValue(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every line the detail pane produces must already fit the pane. The viewport
// counts the lines it is handed but re-wraps them when drawing, so an over-wide
// line silently pushes the bottom of the pane out of reach.
func TestDetailContentFitsThePaneWidth(t *testing.T) {
	const width = 80
	ev := richEvent()
	ev.Payload.Attributes["error"] = "failed to connect to `user=profotograaf database=profotograaf`: " +
		"10.0.0.1:5432 (postgresql-rw.shared-production.svc.cluster.local): dial error: connection refused"
	ev.Payload.Exception.Message = strings.Repeat("a very long exception message ", 8)

	m := newModel(testClient(), "open")
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	m = next.(model)
	next, _ = m.Update(detailMsg{detail: domain.Issue{ID: "BW-3", Title: "wide",
		Fingerprint:            strings.Repeat("f", 64),
		FingerprintMaterial:    strings.Repeat("x", 300),
		FingerprintExplanation: []string{"exception.message=" + strings.Repeat("long ", 30)},
	}})
	m = next.(model)
	m = m.applyEvents(eventsMsg{issueID: "BW-3", events: []domain.Event{ev}})

	for i, line := range strings.Split(m.renderDetailContent(), "\n") {
		if lipgloss.Width(line) > width {
			t.Errorf("line %d is %d cells wide, pane is %d:\n%s", i, lipgloss.Width(line), width, line)
		}
	}
}

func TestWrapIndentsContinuationLines(t *testing.T) {
	got := wrap("alpha beta gamma delta epsilon zeta", "    ", 24)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the text to wrap, got %q", got)
	}
	if strings.HasPrefix(lines[0], " ") {
		t.Error("the first line carries the caller's own prefix and must not be indented")
	}
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "    ") {
			t.Errorf("continuation line not indented: %q", line)
		}
	}
	if unwrapped := wrap("short", "    ", 24); unwrapped != "short" {
		t.Errorf("text that already fits must be left alone, got %q", unwrapped)
	}
}

// The Go SDK puts the frame's own file name in Module, which the location
// already spells out; anything else is real information.
func TestFrameModuleShownOnlyWhenItAddsSomething(t *testing.T) {
	var b strings.Builder
	writeFrame(&b, event.StackFrame{Function: "f", File: "/src/internal/selflog/handler.go",
		Line: 38, Module: "handler.go"})
	if strings.Contains(b.String(), "[handler.go]") {
		t.Errorf("redundant module repeated: %q", b.String())
	}

	b.Reset()
	writeFrame(&b, event.StackFrame{Function: "f", File: "server.go", Line: 1, Module: "api"})
	if !strings.Contains(b.String(), "[api]") {
		t.Errorf("informative module dropped: %q", b.String())
	}
}

func TestTimeAgo(t *testing.T) {
	if got := timeAgo(time.Time{}); got != "never" {
		t.Errorf("zero time = %q, want never", got)
	}
	if got := timeAgo(time.Now().Add(-25 * time.Hour)); got != "1d ago" {
		t.Errorf("25h ago = %q, want 1d ago", got)
	}
	if got := timeAgo(time.Now().Add(-90 * time.Minute)); got != "1h ago" {
		t.Errorf("90m ago = %q, want 1h ago", got)
	}
}
