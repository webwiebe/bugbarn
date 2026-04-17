package bugbarn

import (
	"errors"
	"testing"
	"time"
)

func TestBuildEnvelopeFields(t *testing.T) {
	err := errors.New("something went wrong")
	env := buildEnvelope(err, captureOpts{attributes: make(map[string]any)})

	if env.Timestamp == "" {
		t.Fatal("timestamp must not be empty")
	}
	// Verify it parses as RFC3339.
	if _, parseErr := time.Parse(time.RFC3339Nano, env.Timestamp); parseErr != nil {
		t.Fatalf("invalid timestamp format: %v", parseErr)
	}
	if env.SeverityText != "ERROR" {
		t.Fatalf("expected severityText ERROR, got %s", env.SeverityText)
	}
	if env.Body != "something went wrong" {
		t.Fatalf("unexpected body: %s", env.Body)
	}
	if env.Exception.Type == "" {
		t.Fatal("exception.type must not be empty")
	}
	if env.Exception.Message != "something went wrong" {
		t.Fatalf("unexpected exception.message: %s", env.Exception.Message)
	}
	if env.Sender.SDK.Name != sdkName {
		t.Fatalf("unexpected sdk.name: %s", env.Sender.SDK.Name)
	}
	if env.Sender.SDK.Version != sdkVersion {
		t.Fatalf("unexpected sdk.version: %s", env.Sender.SDK.Version)
	}
}

func TestBuildMessageEnvelope(t *testing.T) {
	env := buildMessageEnvelope("hello world", captureOpts{attributes: make(map[string]any)})

	if env.Body != "hello world" {
		t.Fatalf("unexpected body: %s", env.Body)
	}
	if env.Exception.Type != "Error" {
		t.Fatalf("expected exception.type Error, got %s", env.Exception.Type)
	}
	if env.Exception.Message != "hello world" {
		t.Fatalf("unexpected exception.message: %s", env.Exception.Message)
	}
	if env.SeverityText != "ERROR" {
		t.Fatalf("expected severityText ERROR, got %s", env.SeverityText)
	}
}

func TestBuildPanicEnvelope(t *testing.T) {
	stack := []byte("goroutine 1 [running]:\nmain.main()\n\t/app/main.go:10 +0x40\n")
	env := buildPanicEnvelope("oops", stack)

	if env.Exception.Type != "panic" {
		t.Fatalf("expected type panic, got %s", env.Exception.Type)
	}
	if env.Exception.Message != "oops" {
		t.Fatalf("unexpected message: %s", env.Exception.Message)
	}
	if env.SeverityText != "FATAL" {
		t.Fatalf("expected FATAL severity, got %s", env.SeverityText)
	}
}

func TestCaptureStacktrace(t *testing.T) {
	frames := captureStacktrace(1)
	if frames == nil {
		t.Fatal("expected non-nil stacktrace")
	}
	if len(frames) == 0 {
		t.Fatal("expected at least one frame")
	}
}
