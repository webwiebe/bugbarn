package main

import (
	"bytes"
	"context"
	"errors"
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
		if _, ok, err := verify(context.Background(), key, "alpha"); !ok || err != nil {
			t.Fatalf("the derived setup key must still be accepted: ok=%v err=%v", ok, err)
		}
	}
	if _, ok, _ := verify(context.Background(), setupKey("session-secret", "beta"), "beta"); !ok {
		t.Fatal("beta setup key must be accepted")
	}
	if _, ok, _ := verify(context.Background(), "wrong", "gamma"); ok {
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

func TestSetupKeyRevocationOnlyReadsForAMatchingKey(t *testing.T) {
	reads := 0
	var answer bool
	var readErr error
	verify := withSetupKeyRevocation(newSetupKeyVerifier("session-secret"), func(context.Context, string) (bool, error) {
		reads++
		return answer, readErr
	})
	key := setupKey("session-secret", "alpha")
	ctx := context.Background()

	if _, ok, err := verify(ctx, "wrong", "alpha"); ok || err != nil || reads != 0 {
		t.Fatalf("wrong key: ok=%v err=%v reads=%d", ok, err, reads)
	}
	if _, ok, err := verify(ctx, key, "alpha"); !ok || err != nil {
		t.Fatalf("not revoked: ok=%v err=%v", ok, err)
	}
	answer = true
	if _, ok, err := verify(ctx, key, "alpha"); ok || err != nil {
		t.Fatalf("revoked: ok=%v err=%v", ok, err)
	}
	answer, readErr = false, errors.New("database is locked")
	if _, ok, err := verify(ctx, key, "alpha"); ok || !errors.Is(err, readErr) {
		t.Fatalf("failed read: ok=%v err=%v, want the error and no verdict", ok, err)
	}
}
