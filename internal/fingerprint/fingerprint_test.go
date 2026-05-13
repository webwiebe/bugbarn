package fingerprint

import (
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestFingerprintIgnoresVolatileIdentifiers(t *testing.T) {
	base := event.Event{
		Message: "fallback",
		Exception: event.Exception{
			Type:    "TypeError",
			Message: "cart 550e8400-e29b-41d4-a716-446655440000 failed for user 123456",
			Stacktrace: []event.StackFrame{
				{Function: "checkout", File: "/app/releases/1001/server.ts", Line: 12},
			},
		},
	}
	other := event.Event{
		Message: "fallback",
		Exception: event.Exception{
			Type:    "TypeError",
			Message: "cart 550e8400-e29b-41d4-a716-446655440001 failed for user 987654",
			Stacktrace: []event.StackFrame{
				{Function: "checkout", File: "/app/releases/2002/server.ts", Line: 99},
			},
		},
	}

	if Fingerprint(base) != Fingerprint(other) {
		t.Fatalf("expected matching fingerprints:\n%s\n%s", Material(base), Material(other))
	}
}

func TestFingerprintIgnoresSourceLocations(t *testing.T) {
	base := event.Event{
		Exception: event.Exception{
			Type:    "RuntimeError",
			Message: "[Frontend] SyntaxError: Uncaught SyntaxError: Unexpected identifier 'approve' at https://example.com/recommendations:761:1",
		},
	}
	other := event.Event{
		Exception: event.Exception{
			Type:    "RuntimeError",
			Message: "[Frontend] SyntaxError: Uncaught SyntaxError: Unexpected identifier 'approve' at https://example.com/recommendations:800:15",
		},
	}

	if Fingerprint(base) != Fingerprint(other) {
		t.Fatalf("expected matching fingerprints for different source locations:\n%s\n%s", Material(base), Material(other))
	}
}

func TestFingerprintChangesForDifferentExceptionType(t *testing.T) {
	first := event.Event{Exception: event.Exception{Type: "TypeError", Message: "boom"}}
	second := event.Event{Exception: event.Exception{Type: "ValueError", Message: "boom"}}

	if Fingerprint(first) == Fingerprint(second) {
		t.Fatal("expected different fingerprints")
	}
}

func TestFingerprintFallsBackToRawScrubbed(t *testing.T) {
	// Events with empty exception but different rawScrubbed messages should
	// produce different fingerprints.
	first := event.Event{
		RawScrubbed: map[string]any{
			"exception": map[string]any{},
			"name":      "error",
			"properties": map[string]any{
				"message": "Failed to register a ServiceWorker",
				"source":  "window.onunhandledrejection",
			},
		},
	}
	second := event.Event{
		RawScrubbed: map[string]any{
			"exception": map[string]any{},
			"name":      "error",
			"properties": map[string]any{
				"message": "Network request failed",
				"source":  "window.onerror",
			},
		},
	}

	fp1 := Fingerprint(first)
	fp2 := Fingerprint(second)
	if fp1 == fp2 {
		t.Fatalf("expected different fingerprints for different rawScrubbed messages:\n%s\n%s", Material(first), Material(second))
	}
}

func TestFingerprintRawScrubbedSameMessageProducesSameFingerprint(t *testing.T) {
	// Same error from same source should be deterministic.
	first := event.Event{
		RawScrubbed: map[string]any{
			"name": "error",
			"properties": map[string]any{
				"message": "Failed to register a ServiceWorker",
				"source":  "window.onunhandledrejection",
			},
		},
	}
	second := event.Event{
		RawScrubbed: map[string]any{
			"name": "error",
			"properties": map[string]any{
				"message": "Failed to register a ServiceWorker",
				"source":  "window.onunhandledrejection",
			},
		},
	}

	if Fingerprint(first) != Fingerprint(second) {
		t.Fatalf("expected same fingerprint for identical rawScrubbed:\n%s\n%s", Material(first), Material(second))
	}
}

func TestFingerprintGroupsBareHexIDs(t *testing.T) {
	first := event.Event{Exception: event.Exception{
		Type:    "Error",
		Message: "dead-letter persist: ingest 02c6a89cadf87259a7188a87: database is locked (5) (SQLITE_BUSY)",
	}}
	second := event.Event{Exception: event.Exception{
		Type:    "Error",
		Message: "dead-letter persist: ingest bd26fa35f1b190c93177fe56: database is locked (5) (SQLITE_BUSY)",
	}}
	if Fingerprint(first) != Fingerprint(second) {
		t.Fatalf("expected matching fingerprints:\n%s\n%s", Material(first), Material(second))
	}
}

func TestFingerprintGroupsShortCountVariance(t *testing.T) {
	first := event.Event{Exception: event.Exception{
		Type:    "Error",
		Message: "worker stall: 1 pending, level=degraded",
	}}
	second := event.Event{Exception: event.Exception{
		Type:    "Error",
		Message: "worker stall: 4 pending, level=degraded",
	}}
	if Fingerprint(first) != Fingerprint(second) {
		t.Fatalf("expected matching fingerprints:\n%s\n%s", Material(first), Material(second))
	}
}

func TestFingerprintRawScrubbedNotUsedWhenExceptionPresent(t *testing.T) {
	// When exception has data, rawScrubbed should be ignored for fingerprinting.
	withException := event.Event{
		Exception: event.Exception{Type: "TypeError", Message: "boom"},
		RawScrubbed: map[string]any{
			"name": "error",
			"properties": map[string]any{
				"message": "different message",
			},
		},
	}
	withoutRawScrubbed := event.Event{
		Exception: event.Exception{Type: "TypeError", Message: "boom"},
	}

	if Fingerprint(withException) != Fingerprint(withoutRawScrubbed) {
		t.Fatal("rawScrubbed should not affect fingerprint when exception is present")
	}
}
