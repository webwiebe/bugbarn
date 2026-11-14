package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// statusOf runs one request through the route fixture and returns the status
// and the rest of the described response (headers and body shape).
func (f *routeFixture) statusOf(auth, method, target, body string) (string, string) {
	got := f.do(routeRow{mode: "writer", auth: auth, method: method, target: target, body: body})
	status, rest, _ := strings.Cut(got, "\t")
	return status, rest
}

// Who may do what: each row is one caller against one route, and the status is
// the contract. 401 is for a caller with no credentials, 403 for a credential
// without the scope, and 404 only once the caller is authenticated.
func TestAuthorizationMatrix(t *testing.T) {
	t.Parallel()
	f := newRouteFixture(t)

	cases := []struct {
		auth, method, target, body string
		want                       string
	}{
		{"none", "GET", "/api/v1/issues", "", "401"},
		{"none", "GET", "/api/v1/no-such-route", "", "401"},
		{"none", "POST", "/api/v1/projects", `{"name":"x"}`, "401"},
		{"full", "GET", "/api/v1/no-such-route", "", "404"},
		{"full", "GET", "/api/v1/issues", "", "200"},
		{"read", "GET", "/api/v1/issues", "", "200"},
		{"read", "POST", "/api/v1/projects", `{"name":"x","slug":"x"}`, "403"},
		{"read", "DELETE", "/api/v1/alerts/{alert}", "", "403"},
		{"read", "GET", "/api/v1/settings", "", "403"},
		{"read", "GET", "/api/v1/apikeys", "", "403"},
		{"ingestkey", "GET", "/api/v1/issues", "", "403"},
		{"ingestkey", "GET", "/api/v1/logs", "", "403"},
		{"ingestkey", "POST", "/api/v1/projects", `{"name":"x","slug":"x"}`, "403"},
		{"session", "GET", "/api/v1/settings", "", "200"},
		{"session", "GET", "/api/v1/issues", "", "200"},
	}
	for _, tc := range cases {
		status, rest := f.statusOf(tc.auth, tc.method, tc.target, tc.body)
		if status != tc.want {
			t.Errorf("%s %s %s = %s, want %s (%s)", tc.auth, tc.method, tc.target, status, tc.want, rest)
		}
	}
}

func TestEventEndpointsAnswerForAnAuthenticatedCaller(t *testing.T) {
	t.Parallel()
	f := newRouteFixture(t)

	cases := []struct {
		target, want string
		jsonKeys     string
	}{
		{"/api/v1/issues/{issue}/events?limit=1&before=999999", "200", "json=events,hasMore"},
		{"/api/v1/issues/{issue}/events?limit=9999&before=junk", "200", "json=events,hasMore"},
		{"/api/v1/issues/{issue}/releases", "200", "json=releases,sampled_events"},
		{"/api/v1/events/{event}", "200", "json=event"},
		{"/api/v1/live/events?limit=5&since=2020-01-01T00:00:00Z", "200", "json=events"},
		{"/api/v1/live/events?limit=bad&since=bad", "200", "json=events"},
		{"/api/v1/events/event-999999", "404", ""},
		{"/api/v1/issues/ISS-999999/events", "404", ""},
	}
	for _, tc := range cases {
		status, rest := f.statusOf("full", "GET", tc.target, "")
		if status != tc.want || !strings.Contains(rest, tc.jsonKeys) {
			t.Errorf("GET %s = %s %q, want %s with %q", tc.target, status, rest, tc.want, tc.jsonKeys)
		}
	}
}

func TestStreamSinceParsesTimestamps(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	for query, want := range map[string]time.Time{
		"since=2026-05-01T10:00:00Z":        at,
		"since=2026-05-01T10:00:00.5Z":      at.Add(500 * time.Millisecond),
		"since=2026-05-01T12:00:00%2B02:00": at,
	} {
		got := streamSince(httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if !got.Equal(want) {
			t.Errorf("%s = %v, want %v", query, got, want)
		}
	}
	for _, query := range []string{"", "since=garbage"} {
		got := streamSince(httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if d := time.Since(got); d < 14*time.Minute || d > 16*time.Minute {
			t.Errorf("%q falls back to %v ago, want about 15m", query, d)
		}
	}
}

func TestWriteLiveEventsSkipsOldAndAdvancesCursor(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	events := []domain.Event{ // newest first, as the store returns them
		{ID: "e3", ReceivedAt: base.Add(3 * time.Second)},
		{ID: "e2", ReceivedAt: base.Add(2 * time.Second)},
		{ID: "e1", ReceivedAt: base},
	}
	rr := httptest.NewRecorder()
	next, ok := writeLiveEvents(rr, events, base, slog.Default())
	if !ok {
		t.Fatal("write reported failure")
	}
	if !next.Equal(base.Add(3 * time.Second)) {
		t.Errorf("cursor = %v, want the newest event time", next)
	}
	body := rr.Body.String()
	if strings.Contains(body, `"e1"`) {
		t.Errorf("an event at the cursor must be skipped: %q", body)
	}
	if strings.Index(body, `"e2"`) > strings.Index(body, `"e3"`) || !strings.Contains(body, `"e2"`) {
		t.Errorf("frames must be oldest first: %q", body)
	}
}

func TestTelemetryAndClientErrorEndpoints(t *testing.T) {
	t.Parallel()
	f := newRouteFixture(t)

	spans := `{"spans":[
		{"name":"ok","trace_id":"0af7651916cd43dd8448eb211c80319c","span_id":"b7ad6b7169203331","parent_span_id":"00f067aa0ba902b7","start_time":1700000000000000,"duration":1500,"kind":"SERVER","status":"ERROR","attributes":{"a":"s","b":1.5,"c":true}},
		{"name":"bad-trace","trace_id":"zz","span_id":"b7ad6b7169203331"},
		{"name":"bad-span","trace_id":"0af7651916cd43dd8448eb211c80319c","span_id":"zz"}]}`
	if status, rest := f.statusOf("full", "POST", "/api/v1/telemetry", spans); status != "200" || !strings.Contains(rest, "json=accepted,ok") {
		t.Errorf("telemetry = %s %q, want 200 with accepted", status, rest)
	}
	if status, _ := f.statusOf("full", "POST", "/api/v1/telemetry", "not json"); status != "400" {
		t.Errorf("telemetry with a bad body = %s, want 400", status)
	}
	if status, _ := f.statusOf("full", "POST", "/api/v1/client-errors", `{"message":"boom","type":"TypeError","stack":"at x","url":"/app"}`); status != "200" {
		t.Errorf("client-errors = %s, want 200", status)
	}
	if status, _ := f.statusOf("full", "POST", "/api/v1/client-errors", "[1"); status != "400" {
		t.Errorf("client-errors with a bad body = %s, want 400", status)
	}
}

func TestSpanKindFrom(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"SERVER", "INTERNAL", "PRODUCER", "CONSUMER", "CLIENT", ""} {
		if spanKindFrom(kind).String() == "" {
			t.Errorf("kind %q has no name", kind)
		}
	}
	if spanKindFrom("SERVER") == spanKindFrom("") {
		t.Error("SERVER and the default kind must differ")
	}
}
