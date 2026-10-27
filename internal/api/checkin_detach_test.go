package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/keytouch"
	monitorsvc "github.com/wiebe-xyz/bugbarn/internal/service/monitors"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// hangUpRepo cancels the client's request the moment the store is asked to
// write, which is when a worker with a short timeout hangs up on a slow writer.
type hangUpRepo struct {
	monitorsvc.Repository
	hangUp       context.CancelFunc
	ctxErrAtCall error
	calls        atomic.Int32
}

func (h *hangUpRepo) RecordMonitorCheckIn(ctx context.Context, in storage.MonitorCheckIn) (storage.MonitorCheckInResult, error) {
	h.calls.Add(1)
	h.hangUp()
	h.ctxErrAtCall = ctx.Err()
	return h.Repository.RecordMonitorCheckIn(ctx, in)
}

func TestCheckInIsStoredOnceAfterTheClientHangsUp(t *testing.T) {
	t.Parallel()
	h := newMonitorHarness(t)
	repo := &hangUpRepo{Repository: h.store.MonitorStore}
	h.server.monitors = monitorsvc.New(repo, nil)

	send := func() *httptest.ResponseRecorder {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		repo.hangUp = cancel
		req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/job/check-ins",
			strings.NewReader(`{"status":"ok","interval":"1h"}`)).WithContext(ctx)
		req.Header.Set("X-BugBarn-API-Key", h.key)
		rr := httptest.NewRecorder()
		h.server.ServeHTTP(rr, req)
		return rr
	}

	if rr := send(); rr.Code != http.StatusCreated {
		t.Fatalf("first beat after a hang-up = %d, want 201: %s", rr.Code, rr.Body)
	}
	if repo.ctxErrAtCall != nil {
		t.Fatalf("the store saw a canceled context: %v", repo.ctxErrAtCall)
	}
	// The worker retries the beat it believes was lost.
	if rr := send(); rr.Code != http.StatusOK {
		t.Fatalf("retried beat = %d, want 200: %s", rr.Code, rr.Body)
	}

	geo, err := h.store.ProjectBySlug(context.Background(), "geo")
	if err != nil {
		t.Fatal(err)
	}
	list, err := h.store.ListMonitors(context.Background(), geo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("%d monitors after a beat and its retry, want 1", len(list))
	}
	if list[0].State != domain.MonitorStateOK || list[0].LastStatus != domain.CheckInOK || list[0].LastCheckInAt.IsZero() {
		t.Fatalf("monitor after the beats: state %q status %q last %v", list[0].State, list[0].LastStatus, list[0].LastCheckInAt)
	}
	if repo.calls.Load() != 2 {
		t.Fatalf("store writes = %d, want one per request", repo.calls.Load())
	}
}

func TestForwardDetachedFinishesAfterTheClientHangsUp(t *testing.T) {
	t.Parallel()
	var arrived, finished, canceled atomic.Int32
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Add(1)
		select {
		case <-time.After(300 * time.Millisecond):
			finished.Add(1)
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			canceled.Add(1)
		}
	}))
	t.Cleanup(writer.Close)
	fwd := NewWriteForwarder(writer.URL)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/job/check-ins",
		strings.NewReader(`{"status":"ok"}`)).WithContext(ctx)
	rr := httptest.NewRecorder()
	time.AfterFunc(50*time.Millisecond, cancel)
	fwd.ForwardDetached(rr, req, 5*time.Second)

	if arrived.Load() != 1 || finished.Load() != 1 || canceled.Load() != 0 {
		t.Fatalf("writer saw arrived=%d finished=%d canceled=%d, want 1/1/0", arrived.Load(), finished.Load(), canceled.Load())
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("answer = %d, want the writer's 200", rr.Code)
	}
}

func TestForwardDetachedGivesUpAtItsOwnDeadline(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(writer.Close)
	t.Cleanup(func() { close(release) })
	fwd := NewWriteForwarder(writer.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/job/check-ins", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	start := time.Now()
	fwd.ForwardDetached(rr, req, 150*time.Millisecond)

	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("a stalled writer held the handler for %v", took)
	}
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("answer = %d, want 502 once the deadline passes", rr.Code)
	}
}

// touchProbe records whether the context it was handed was still live when the
// writer applied the batch, after the reader's request had been canceled.
type touchProbe struct {
	ctxErr  error
	touches int
}

func (p *touchProbe) TouchAPIKeysAt(ctx context.Context, touches []domain.APIKeyTouch) error {
	p.ctxErr = ctx.Err()
	p.touches = len(touches)
	return nil
}

func TestKeyTouchBatchIsAppliedAfterTheReaderHangsUp(t *testing.T) {
	t.Parallel()
	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewServerWithAuth(nil, mustOpenStore(t), userAuth, auth.NewSessionManager(keyTouchSecret, time.Hour), nil, nil)
	writer.SetInternalSessionSecret(keyTouchSecret)
	probe := &touchProbe{}
	writer.SetKeyToucher(probe)

	body, err := json.Marshal(keytouch.Batch{TS: time.Now().Unix(), Touches: []domain.APIKeyTouch{{KeySHA256: "hash-k", At: time.Now().UTC()}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, keytouch.Path, bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set(sessionstore.AuthHeader, sessionstore.SignBody([]byte(keyTouchSecret), body))
	rr := httptest.NewRecorder()
	writer.ServeHTTP(rr, req)

	if probe.touches != 1 || probe.ctxErr != nil {
		t.Fatalf("apply saw touches=%d ctx err=%v, want 1 live touch", probe.touches, probe.ctxErr)
	}
}
