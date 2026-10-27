package ingesthealth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A pipeline that was unhealthy logs when it clears, so a stall has an end in
// the log. A blip that never reached ConfirmSamples is marked as not alerted.
func TestRecoveryIsLogged(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 25, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		stalled int
		alerted bool
	}{
		{"blip", 1, false},
		{"confirmed stall", 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			depth := int64(2)
			var logs bytes.Buffer
			m := New(Config{StaleAfter: 30 * time.Minute, Environment: "production"},
				rolloutDeps(now.Add(-40*time.Minute), &depth),
				slog.New(slog.NewJSONHandler(&logs, nil)))
			cur := now
			m.now = func() time.Time { return cur }

			for i := 0; i < tc.stalled; i++ {
				m.sample(context.Background())
				cur = cur.Add(time.Minute)
			}
			if strings.Contains(logs.String(), "recovered") {
				t.Fatalf("no recovery line while unhealthy:\n%s", logs.String())
			}

			depth = 0
			m.sample(context.Background())
			out := logs.String()
			if got := strings.Count(out, "ingest-health: recovered"); got != 1 {
				t.Fatalf("want one recovery line, got %d:\n%s", got, out)
			}
			want := `"alerted":false`
			if tc.alerted {
				want = `"alerted":true`
			}
			if !strings.Contains(out, want) {
				t.Fatalf("recovery line must carry %s:\n%s", want, out)
			}

			logs.Reset()
			cur = cur.Add(time.Minute)
			m.sample(context.Background())
			if strings.Contains(logs.String(), "recovered") {
				t.Fatalf("a healthy sample after recovery must stay silent:\n%s", logs.String())
			}
		})
	}
}
