package alert

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type errNotifier struct{ err error }

func (n errNotifier) EmailConfigured() bool { return true }

func (n errNotifier) Fire(context.Context, Rule, domain.Issue, string) error { return n.err }

func waitForLog(buf *syncBuffer, want string) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestEvaluator_AdminDeliveryOutcomeIsLogged(t *testing.T) {
	t.Parallel()

	t.Run("success logs a send line without the recipient", func(t *testing.T) {
		t.Parallel()
		buf := &syncBuffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))
		e := NewEvaluator(newFakeRepo(nil), &fakeNotifier{configured: true}, "http://x", "admin@example.com", log)

		e.notifyAdmin(storage.Issue{ID: "issue-1", ProjectSlug: "demo"}, "new_issue")

		if !waitForLog(buf, `"msg":"admin alert sent"`) {
			t.Fatalf("no send line in log: %s", buf.String())
		}
		out := buf.String()
		for _, want := range []string{`"project":"demo"`, `"condition":"new_issue"`, `"issue_id":"issue-1"`} {
			if !strings.Contains(out, want) {
				t.Errorf("log line misses %s: %s", want, out)
			}
		}
		if strings.Contains(out, "admin@example.com") {
			t.Errorf("recipient address leaked into the log: %s", out)
		}
	})

	t.Run("failure logs at ERROR and no send line", func(t *testing.T) {
		t.Parallel()
		buf := &syncBuffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))
		e := NewEvaluator(newFakeRepo(nil), errNotifier{err: errors.New("smtp down")}, "http://x", "admin@example.com", log)

		e.notifyAdmin(storage.Issue{ID: "issue-1"}, "regression")

		if !waitForLog(buf, `"msg":"failed to fire admin alert"`) {
			t.Fatalf("no failure line in log: %s", buf.String())
		}
		out := buf.String()
		if !strings.Contains(out, `"level":"ERROR"`) {
			t.Errorf("delivery failure is not logged at ERROR: %s", out)
		}
		if strings.Contains(out, "admin alert sent") {
			t.Errorf("failure also logged a send line: %s", out)
		}
	})
}

func TestEvaluator_RuleDeliveryFailureLogsError(t *testing.T) {
	t.Parallel()

	buf := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))
	repo := newFakeRepo([]Rule{{ID: "alert-1", Enabled: true, Condition: "new_issue", EmailTo: "x@example.com"}})
	e := NewEvaluator(repo, errNotifier{err: errors.New("smtp down")}, "http://x", "", log)

	e.evaluate(context.Background(), 1, storage.Issue{ID: "issue-1"}, "new_issue")

	if !waitForLog(buf, `"msg":"failed to fire alert"`) {
		t.Fatalf("no failure line in log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("rule delivery failure is not logged at ERROR: %s", buf.String())
	}
}
