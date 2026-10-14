package keytouch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestBatcher(send Sender) (*Batcher, *clock) {
	b := NewBatcher(send, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := &clock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	b.now = c.now
	return b, c
}

func TestFlushSendsOneEntryPerKeyWithTheLatestUse(t *testing.T) {
	var got []domain.APIKeyTouch
	b, c := newTestBatcher(func(_ context.Context, touches []domain.APIKeyTouch) error {
		got = touches
		return nil
	})
	_ = b.Touch(context.Background(), "aaa")
	c.t = c.t.Add(time.Minute)
	_ = b.Touch(context.Background(), "aaa")
	_ = b.Touch(context.Background(), "bbb")

	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("batch = %v, want one entry per key", got)
	}
	for _, touch := range got {
		if touch.KeySHA256 == "aaa" && !touch.At.Equal(c.t) {
			t.Fatalf("aaa At = %v, want the later use %v", touch.At, c.t)
		}
	}
	if err := b.Flush(context.Background()); err != nil || len(got) != 2 {
		t.Fatalf("a second flush with nothing pending must not send (err=%v)", err)
	}
}

func TestFlushKeepsTouchesWhenTheWriterIsUnreachable(t *testing.T) {
	fail := true
	var sent []domain.APIKeyTouch
	b, c := newTestBatcher(func(_ context.Context, touches []domain.APIKeyTouch) error {
		if fail {
			return errors.New("writer down")
		}
		sent = touches
		return nil
	})
	_ = b.Touch(context.Background(), "aaa")
	first := c.t
	if err := b.Flush(context.Background()); err == nil {
		t.Fatal("want the send error")
	}
	c.t = c.t.Add(time.Minute)
	_ = b.Touch(context.Background(), "aaa") // used again while the writer was down

	fail = false
	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !sent[0].At.After(first) {
		t.Fatalf("sent = %v, want the newer use of aaa", sent)
	}
}

func TestPendingIsBounded(t *testing.T) {
	b, _ := newTestBatcher(nil)
	for i := 0; i < maxPending+50; i++ {
		_ = b.Touch(context.Background(), string(rune('a'+i%26))+time.Duration(i).String())
	}
	if len(b.pending) > maxPending {
		t.Fatalf("pending = %d, want at most %d", len(b.pending), maxPending)
	}
}

func TestRemoteSenderSignsTheBatchForTheWriter(t *testing.T) {
	var mu sync.Mutex
	var path, header string
	var verified bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		path, header = r.URL.Path, r.Header.Get(sessionstore.AuthHeader)
		verified = sessionstore.VerifyBody([]byte("secret"), body, header)
	}))
	defer srv.Close()

	send := NewRemoteSender(srv.URL+"/", "secret")
	if err := send(context.Background(), []domain.APIKeyTouch{{KeySHA256: "aaa", At: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if path != Path || !verified {
		t.Fatalf("path=%q verified=%v", path, verified)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer bad.Close()
	if err := NewRemoteSender(bad.URL, "secret")(context.Background(), nil); err == nil {
		t.Fatal("a rejected batch must be an error so the touches are kept")
	}
}
