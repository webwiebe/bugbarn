package normalize

import (
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
