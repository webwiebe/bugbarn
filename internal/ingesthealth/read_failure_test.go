package ingesthealth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// One failed read leaves a sample without that signal and the next tick reads
// it again, so it logs a warning. Only a streak is an error, because only a
// streak leaves the monitor blind.
func TestSampleReadFailureErrorsOnlyOnStreak(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	fail := true
	deps := Deps{LastEventAt: func(context.Context) (time.Time, error) {
		if fail {
			return time.Time{}, errors.New("locking protocol (15)")
		}
		return time.Now(), nil
	}}
	m := New(Config{StaleAfter: time.Hour}, deps, logger)

	for i := 1; i <= readFailureErrorAfter; i++ {
		buf.Reset()
		m.sample(context.Background())
		isError := strings.Contains(buf.String(), `"level":"ERROR"`)
		if isError != (i == readFailureErrorAfter) {
			t.Fatalf("failure %d: error level = %v, log %s", i, isError, buf.String())
		}
	}

	fail = false
	m.sample(context.Background())
	fail = true
	buf.Reset()
	m.sample(context.Background())
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("a success must reset the streak: %s", buf.String())
	}
}
