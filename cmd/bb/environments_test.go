package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestEnvironmentsListsProjectEnvironments(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)

	out, err := runCmd(t, cmdEnvironments, "--project", "default")
	if err != nil {
		t.Fatalf("empty list: %v\n%s", err, out)
	}
	var empty struct {
		Environments []map[string]any `json:"environments"`
	}
	if err := json.Unmarshal([]byte(out), &empty); err != nil || empty.Environments == nil || len(empty.Environments) != 0 {
		t.Fatalf("empty = %s (%v), want an empty list", out, err)
	}

	now := time.Now().UTC()
	pe := event.ProcessedEvent{
		Event: event.Event{
			ObservedAt: now, ReceivedAt: now.Add(time.Second), Severity: "ERROR", Message: "boom",
			Exception:  event.Exception{Type: "TestError", Message: "boom"},
			Attributes: map[string]any{"deployment.environment": "staging"},
		},
		Fingerprint: "fp-env", FingerprintMaterial: "TestError: boom",
	}
	if _, _, _, _, err := h.Store.PersistProcessedEvent(context.Background(), pe); err != nil {
		t.Fatalf("persist: %v", err)
	}
	out, err = runCmd(t, cmdEnvironments, "--project", "default")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	var got struct {
		Environments []struct {
			Environment string `json:"environment"`
			Issues      int    `json:"issues"`
		} `json:"environments"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(got.Environments) != 1 || got.Environments[0].Environment != "staging" || got.Environments[0].Issues != 1 {
		t.Fatalf("environments = %+v, want one staging row with one issue", got.Environments)
	}
}
