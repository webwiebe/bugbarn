package normalize

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestNormalizeOpenTelemetryShapedEvent(t *testing.T) {
	raw, err := os.ReadFile("../../specs/001-personal-error-tracker/fixtures/example-event.json")
	if err != nil {
		t.Fatal(err)
	}

	received := time.Date(2026, 4, 15, 8, 31, 0, 0, time.UTC)
	evt, err := Normalize(raw, "ing-1", received)
	if err != nil {
		t.Fatal(err)
	}

	if evt.IngestID != "ing-1" {
		t.Fatalf("unexpected ingest id: %s", evt.IngestID)
	}
	if evt.Exception.Type != "TypeError" {
		t.Fatalf("unexpected exception type: %s", evt.Exception.Type)
	}
	if evt.Exception.Message != "Cannot read properties of undefined reading 'total' for cart [redacted-id]" {
		t.Fatalf("exception message was not scrubbed: %q", evt.Exception.Message)
	}
	if evt.Attributes["enduser.id"] != "user-12345" {
		t.Fatalf("unexpected attribute: %#v", evt.Attributes["enduser.id"])
	}
	if evt.SDKName != "bugbarn.typescript" {
		t.Fatalf("unexpected sdk name: %s", evt.SDKName)
	}
	if len(evt.Exception.Stacktrace) != 2 {
		t.Fatalf("unexpected frame count: %d", len(evt.Exception.Stacktrace))
	}
}

func TestNormalizeSDKStyleEvent(t *testing.T) {
	raw := []byte(`{
		"message": "failed for user@example.com",
		"exception": {"type": "ValueError", "value": "bad token: abcdefghijklmnop"},
		"tags": {"route": "/users/123"},
		"extra": {"ip": "10.0.0.1"},
		"sender": {"sdk": {"name": "bugbarn.python", "version": "0.1.0"}}
	}`)

	evt, err := Normalize(raw, "ing-2", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if evt.Message != "failed for [redacted-email]" {
		t.Fatalf("message was not scrubbed: %q", evt.Message)
	}
	if evt.Exception.Message != "bad [redacted-secret]" {
		t.Fatalf("exception was not scrubbed: %q", evt.Exception.Message)
	}
	if evt.SDKName != "bugbarn.python" {
		t.Fatalf("unexpected sdk name: %s", evt.SDKName)
	}
}

func TestNormalizeUserExtraction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		raw          string
		wantID       string
		wantUsername string
	}{
		{
			name:         "full user object",
			raw:          `{"message":"err","user":{"id":"u123","email":"alice@example.com","username":"alice"}}`,
			wantID:       "u123",
			wantUsername: "alice",
		},
		{
			name:         "user_id fallback",
			raw:          `{"message":"err","user":{"user_id":"u456","name":"bob"}}`,
			wantID:       "u456",
			wantUsername: "bob",
		},
		{
			name: "no user field",
			raw:  `{"message":"err"}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			evt, err := Normalize([]byte(tc.raw), "ing-test", time.Now())
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if evt.User.ID != tc.wantID {
				t.Errorf("User.ID: got %q, want %q", evt.User.ID, tc.wantID)
			}
			if tc.wantUsername != "" && evt.User.Username != tc.wantUsername {
				t.Errorf("User.Username: got %q, want %q", evt.User.Username, tc.wantUsername)
			}
		})
	}
}

func TestNormalizeBreadcrumbExtraction(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"message": "crash",
		"breadcrumbs": [
			{"timestamp": "2026-01-01T00:00:00Z", "category": "navigation", "message": "navigated to /home"},
			{"timestamp": "2026-01-01T00:00:01Z", "category": "http", "message": "GET /api/data", "level": "info", "data": {"status": 200}},
			{"category": "ui", "message": "button clicked"}
		]
	}`)

	evt, err := Normalize(raw, "ing-bc", time.Now())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(evt.Breadcrumbs) != 3 {
		t.Fatalf("expected 3 breadcrumbs, got %d", len(evt.Breadcrumbs))
	}
	if evt.Breadcrumbs[0].Category != "navigation" {
		t.Errorf("first breadcrumb category: got %q, want %q", evt.Breadcrumbs[0].Category, "navigation")
	}
	if evt.Breadcrumbs[1].Level != "info" {
		t.Errorf("second breadcrumb level: got %q, want %q", evt.Breadcrumbs[1].Level, "info")
	}
	if evt.Breadcrumbs[1].Data == nil {
		t.Error("expected breadcrumb data to be populated")
	}
}

func TestNormalizeBreadcrumbCap(t *testing.T) {
	t.Parallel()

	// Build a payload with 150 breadcrumbs.
	crumbs := make([]any, 150)
	for i := range crumbs {
		crumbs[i] = map[string]any{
			"timestamp": "2026-01-01T00:00:00Z",
			"message":   "item",
		}
	}
	payload := map[string]any{
		"message":     "overflow",
		"breadcrumbs": crumbs,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	evt, err := Normalize(raw, "ing-cap", time.Now())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(evt.Breadcrumbs) != 100 {
		t.Errorf("expected breadcrumbs capped at 100, got %d", len(evt.Breadcrumbs))
	}
}
