package sessionstore

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakePruner struct {
	n   int64
	err error
}

func (p *fakePruner) PruneWebSessions(context.Context, time.Time) (int64, error) {
	return p.n, p.err
}

func bufLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

func TestPruneOnceLogsTheOutcome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	log, buf := bufLogger()
	pruneOnce(context.Background(), &fakePruner{n: 3}, log, now)
	if !strings.Contains(buf.String(), "pruned expired web sessions") || !strings.Contains(buf.String(), "count=3") {
		t.Fatalf("log = %q", buf.String())
	}

	log, buf = bufLogger()
	pruneOnce(context.Background(), &fakePruner{}, log, now)
	if buf.Len() != 0 {
		t.Fatalf("nothing pruned logged %q", buf.String())
	}

	log, buf = bufLogger()
	pruneOnce(context.Background(), &fakePruner{err: errors.New("locked")}, log, now)
	if !strings.Contains(buf.String(), "web-session prune failed") {
		t.Fatalf("log = %q", buf.String())
	}
}

func TestPruneOnceRemovesExpiredSessionsFromTheStore(t *testing.T) {
	t.Parallel()
	d, db := newDirect(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	old := oidcSession("old", now.Add(-48*time.Hour), now.Add(-47*time.Hour))
	old.AbsoluteExpiresAt = now.Add(-time.Hour)
	if err := d.Create(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := d.Create(ctx, oidcSession("fresh", now, now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	log, _ := bufLogger()
	pruneOnce(ctx, db, log, now)
	if _, err := d.Get(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session after prune: %v", err)
	}
	if _, err := d.Get(ctx, "fresh"); err != nil {
		t.Fatalf("live session after prune: %v", err)
	}
}

func TestStartPrunerStopsWhenCanceled(t *testing.T) {
	t.Parallel()
	log, _ := bufLogger()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	StartPruner(ctx, &fakePruner{}, log, &wg)
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("pruner did not stop after cancel")
	}
}

func TestSetRefresherReplacesTheRefresher(t *testing.T) {
	t.Parallel()
	d, _ := newDirect(t, nil)
	r := &fakeRefresher{}
	d.SetRefresher(r)
	if d.oidc != TokenRefresher(r) {
		t.Fatal("SetRefresher did not install the refresher")
	}
}
