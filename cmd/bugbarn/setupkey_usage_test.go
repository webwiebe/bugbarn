package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestSetupKeyUsageLogsEachProjectOnceAndNeverTheKey(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	verify := withSetupKeyUsage(newSetupKeyVerifier("session-secret"), logger)

	key := setupKey("session-secret", "alpha")
	for i := 0; i < 3; i++ {
		if _, ok := verify(context.Background(), key, "alpha"); !ok {
			t.Fatal("the derived setup key must still be accepted")
		}
	}
	if _, ok := verify(context.Background(), setupKey("session-secret", "beta"), "beta"); !ok {
		t.Fatal("beta setup key must be accepted")
	}
	if _, ok := verify(context.Background(), "wrong", "gamma"); ok {
		t.Fatal("a wrong key must stay rejected")
	}

	out := buf.String()
	if got := strings.Count(out, "project=alpha"); got != 1 {
		t.Fatalf("alpha logged %d times, want once:\n%s", got, out)
	}
	if !strings.Contains(out, "project=beta") {
		t.Fatalf("beta missing from the log:\n%s", out)
	}
	if strings.Contains(out, "gamma") {
		t.Fatalf("a rejected request must not be logged:\n%s", out)
	}
	if strings.Contains(out, key) {
		t.Fatalf("the setup key leaked into the log:\n%s", out)
	}
}
