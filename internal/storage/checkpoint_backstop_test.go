package storage

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func capturingLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// When TRUNCATE keeps losing to reader snapshots the WAL must still be bounded:
// after enough consecutive failed TRUNCATE ticks with an oversized WAL, one
// TRUNCATE with the full wait runs and is logged at warn.
func TestCheckpointBackstopFiresAfterRepeatedFailures(t *testing.T) {
	oldTicks, oldBytes, oldWait := backstopAfterTicks, backstopBytes, backstopWait
	backstopAfterTicks, backstopBytes, backstopWait = 2, 1, 3*time.Second
	defer func() { backstopAfterTicks, backstopBytes, backstopWait = oldTicks, oldBytes, oldWait }()

	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	release := pinSnapshot(t, store, dbPath)
	defer release()

	log, buf := capturingLogger()
	policy := CheckpointPolicy{
		TruncateEvery: 1, TruncateBusyTimeout: 200 * time.Millisecond, WALSize: WALFileSize(dbPath),
	}
	st := &checkpointState{}
	tick := func() {
		// A 1s budget ends before the 5s retry interval: one attempt per tick.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		checkpointTick(ctx, store.db, 1, st, policy, log)
	}

	tick()
	if strings.Contains(buf.String(), "backstop") {
		t.Fatalf("backstop fired after 1 failed tick, threshold is %d:\n%s", backstopAfterTicks, buf.String())
	}
	if st.failedTruncates != 1 {
		t.Fatalf("failedTruncates = %d, want 1", st.failedTruncates)
	}
	if walSize(t, dbPath) == 0 {
		t.Fatal("WAL was truncated despite a pinned snapshot; the setup does not block TRUNCATE")
	}

	// The second failed tick reaches the threshold. The reader lets go while the
	// backstop waits, so its full-wait TRUNCATE can finish.
	go func() {
		time.Sleep(700 * time.Millisecond)
		release()
	}()
	tick()

	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "backstop") {
		t.Errorf("expected a warn-level backstop log, got:\n%s", out)
	}
	if got := walSize(t, dbPath); got != 0 {
		t.Errorf("WAL size after backstop = %d, want 0", got)
	}
	if st.failedTruncates != 0 {
		t.Errorf("failedTruncates = %d after a successful truncate, want 0", st.failedTruncates)
	}
}

// Every TRUNCATE tick logs its result.
func TestCheckpointTickLogsResult(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	growWAL(t, store)

	log, buf := capturingLogger()
	st := &checkpointState{failedTruncates: 2}
	checkpointTick(context.Background(), store.db, 1, st, LegacyCheckpointPolicy(), log)

	out := buf.String()
	for _, want := range []string{"mode=TRUNCATE", "result=truncated", "failed_truncate_ticks=0"} {
		if !strings.Contains(out, want) {
			t.Errorf("tick log missing %q:\n%s", want, out)
		}
	}
	if st.failedTruncates != 0 {
		t.Errorf("failedTruncates = %d, want 0 after a truncate", st.failedTruncates)
	}
}
