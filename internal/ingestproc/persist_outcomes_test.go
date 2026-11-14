package ingestproc

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestConsumerPersistEventLabelsEveryOutcome(t *testing.T) {
	t.Parallel()
	good := queue.Item{IngestID: "i1", ReceivedAt: time.Now().UTC(), BodyBase64: eventBody(t), ProjectSlug: "outcomes"}
	garbage := queue.Item{IngestID: "i2", ReceivedAt: time.Now().UTC(), BodyBase64: base64.StdEncoding.EncodeToString([]byte("not json")), ProjectSlug: "outcomes"}

	t.Run("success", func(t *testing.T) {
		proc, _ := newProcessor(t)
		c := NewConsumer(nil, proc, nil, nil, nil)
		if got := c.persistEvent(context.Background(), good); got != "success" {
			t.Errorf("outcome = %q, want success", got)
		}
	})
	t.Run("decode error", func(t *testing.T) {
		proc, _ := newProcessor(t)
		c := NewConsumer(nil, proc, nil, nil, nil)
		if got := c.persistEvent(context.Background(), queue.Item{BodyBase64: "!!not base64!!"}); got != "decode_error" {
			t.Errorf("outcome = %q, want decode_error", got)
		}
	})
	t.Run("parse error", func(t *testing.T) {
		proc, _ := newProcessor(t)
		c := NewConsumer(nil, proc, nil, nil, nil)
		if got := c.persistEvent(context.Background(), garbage); got != "parse_error" {
			t.Errorf("outcome = %q, want parse_error", got)
		}
	})
	t.Run("held", func(t *testing.T) {
		proc, _ := pendingProcessor(t)
		c := NewConsumer(nil, proc, nil, nil, nil)
		if got := c.persistEvent(context.Background(), good); got != "held" {
			t.Errorf("outcome = %q, want held", got)
		}
	})
	t.Run("persist error", func(t *testing.T) {
		proc, store := newProcessor(t)
		store.Close()
		c := NewConsumer(nil, proc, nil, nil, nil)
		if got := c.persistEvent(context.Background(), good); got != "persist_error" {
			t.Errorf("outcome = %q, want persist_error", got)
		}
	})
	t.Run("transient on a spent deadline stops at once", func(t *testing.T) {
		proc, _ := newProcessor(t)
		c := NewConsumer(nil, proc, nil, nil, nil)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		if got := c.persistEvent(ctx, good); got != "transient_drop" {
			t.Errorf("outcome = %q, want transient_drop", got)
		}
	})
}

func TestReplayOneHandlesEveryKind(t *testing.T) {
	t.Parallel()
	proc, store := newProcessor(t)
	r := NewReplayer(store, proc, nil, nil)
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("event persists", func(t *testing.T) {
		rec := storage.HeldRecord{Kind: storage.HeldKindEvent, IngestID: "e1", ReceivedAt: now, BodyBase64: eventBody(t), Slug: "replay-one"}
		if err := r.replayOne(ctx, rec); err != nil {
			t.Errorf("replay event: %v", err)
		}
	})
	t.Run("unparseable event is dropped", func(t *testing.T) {
		rec := storage.HeldRecord{Kind: storage.HeldKindEvent, IngestID: "e2", ReceivedAt: now, BodyBase64: base64.StdEncoding.EncodeToString([]byte("nope")), Slug: "replay-one"}
		if err := r.replayOne(ctx, rec); err != nil {
			t.Errorf("a parse error must not block the drain: %v", err)
		}
	})
	t.Run("a persist failure is returned for a later retry", func(t *testing.T) {
		failProc, failStore := newProcessor(t)
		failStore.Close()
		rec := storage.HeldRecord{Kind: storage.HeldKindEvent, IngestID: "e3", ReceivedAt: now, BodyBase64: eventBody(t), Slug: "replay-one"}
		if err := NewReplayer(failStore, failProc, nil, nil).replayOne(ctx, rec); err == nil {
			t.Error("a failed persist must be returned so the record stays held")
		}
	})
	t.Run("log without an inserter is skipped", func(t *testing.T) {
		rec := storage.HeldRecord{Kind: storage.HeldKindLog, ID: 7, BodyBase64: base64.StdEncoding.EncodeToString([]byte(`{"message":"x"}`))}
		if err := r.replayOne(ctx, rec); err != nil {
			t.Errorf("replay log: %v", err)
		}
	})
	t.Run("undecodable log is dropped", func(t *testing.T) {
		withLogs := NewReplayer(store, proc, &scriptedLogs{}, nil)
		rec := storage.HeldRecord{Kind: storage.HeldKindLog, ID: 8, BodyBase64: "!!"}
		if err := withLogs.replayOne(ctx, rec); err != nil {
			t.Errorf("an undecodable log must not block the drain: %v", err)
		}
	})
	t.Run("unknown kind is dropped", func(t *testing.T) {
		if err := r.replayOne(ctx, storage.HeldRecord{Kind: "mystery", ID: 9}); err != nil {
			t.Errorf("unknown kind: %v", err)
		}
	})
}
