package keytouch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestRunFlushesWhatIsPendingWhenTheContextEnds(t *testing.T) {
	var mu sync.Mutex
	var sent []domain.APIKeyTouch
	delivered := make(chan struct{})
	b, _ := newTestBatcher(func(_ context.Context, touches []domain.APIKeyTouch) error {
		mu.Lock()
		sent = append(sent, touches...)
		mu.Unlock()
		close(delivered)
		return nil
	})
	if err := b.Touch(context.Background(), "aaa"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not flush the pending touch")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the context ended")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0].KeySHA256 != "aaa" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestRemoteSenderReportsAnUnreachableWriter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	send := NewRemoteSender(url, "secret")
	err := send(context.Background(), []domain.APIKeyTouch{{KeySHA256: "aaa", At: time.Now()}})
	if err == nil {
		t.Fatal("a writer that refuses connections must be an error")
	}
}

func TestRemoteSenderRejectsAMalformedWriterURL(t *testing.T) {
	send := NewRemoteSender("http://bad\x7fhost", "secret")
	if err := send(context.Background(), nil); err == nil {
		t.Fatal("a malformed writer URL must be an error")
	}
}
