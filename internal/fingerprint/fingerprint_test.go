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

func TestFingerprintChangesForDifferentExceptionType(t *testing.T) {
	first := event.Event{Exception: event.Exception{Type: "TypeError", Message: "boom"}}
	second := event.Event{Exception: event.Exception{Type: "ValueError", Message: "boom"}}

	if Fingerprint(first) == Fingerprint(second) {
		t.Fatal("expected different fingerprints")
	}
}
