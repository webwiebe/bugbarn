package ingestproc

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

type fakeTelemetryIngester struct {
	calls int
	body  string
	err   error
}

func (f *fakeTelemetryIngester) Ingest(_ context.Context, _ string, body []byte) error {
	f.calls++
	f.body = string(body)
	return f.err
}

func TestPersistTelemetry(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	c := NewConsumer(nil, nil, nil, &mu, nil)
	item := queue.Item{Kind: queue.KindSecurity, BodyBase64: base64.StdEncoding.EncodeToString([]byte("LINE"))}

	if got := c.persistTelemetry(context.Background(), item); got != "dropped" {
		t.Fatalf("no ingester: outcome %q, want dropped", got)
	}

	tel := &fakeTelemetryIngester{}
	c.SetTelemetry(tel)
	if got := c.persistTelemetry(context.Background(), item); got != "success" || tel.body != "LINE" {
		t.Fatalf("outcome %q body %q", got, tel.body)
	}
	if got := c.persistTelemetry(context.Background(), queue.Item{Kind: queue.KindMetrics, BodyBase64: "%%%"}); got != "decode_error" {
		t.Fatalf("bad base64: outcome %q", got)
	}

	// One attempt only: a retry would feed detections the same lines twice.
	tel.err, tel.calls = errors.New("disk I/O error"), 0
	if got := c.persistTelemetry(context.Background(), item); got != "insert_error" || tel.calls != 1 {
		t.Fatalf("insert error: outcome %q after %d calls, want insert_error after 1", got, tel.calls)
	}
}
