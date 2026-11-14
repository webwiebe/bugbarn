package ingest

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func TestNewHandlerDefaultsTheBodyLimit(t *testing.T) {
	t.Parallel()
	h := NewHandler(auth.New("k"), mustSpool(t), 0)
	if got := h.MaxBodyBytes(); got != 1<<20 {
		t.Fatalf("MaxBodyBytes = %d, want %d", got, 1<<20)
	}
	if got := NewHandler(auth.New("k"), mustSpool(t), 77).MaxBodyBytes(); got != 77 {
		t.Fatalf("MaxBodyBytes = %d, want 77", got)
	}
}

func TestStartReturnsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	h := NewHandler(auth.New("k"), mustSpool(t), 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after the context ended")
	}
}

func TestValidAPIKeyAndProjectLookup(t *testing.T) {
	t.Parallel()
	h := NewHandler(auth.New("secret"), mustSpool(t), 0)

	good := httptest.NewRequest(http.MethodPost, "/x", nil)
	good.Header.Set(auth.HeaderAPIKey, "secret")
	if !h.ValidAPIKey(good) {
		t.Error("the configured key must be valid")
	}
	if pid, ok := h.APIKeyProject(good); !ok || pid != 0 {
		t.Errorf("APIKeyProject = %d, %v; want 0, true for a static key", pid, ok)
	}

	bad := httptest.NewRequest(http.MethodPost, "/x", nil)
	bad.Header.Set(auth.HeaderAPIKey, "wrong")
	if h.ValidAPIKey(bad) {
		t.Error("a wrong key must be invalid")
	}
}

func TestNilAuthorizerAcceptsEveryKeyWithFullScope(t *testing.T) {
	t.Parallel()
	var h *Handler
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	pid, scope, ok, err := h.CheckAPIKey(req)
	if pid != 0 || scope != "full" || !ok || err != nil {
		t.Fatalf("CheckAPIKey = %d, %q, %v, %v", pid, scope, ok, err)
	}
}

func TestSpoolReleaseWritesAReleaseRecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sp, err := spool.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	h := NewHandler(auth.New("k"), sp, 0)
	h.idFn = func() string { return "rel-1" }

	id, err := h.SpoolRelease(42, "application/json", "10.0.0.1:1", []byte(`{"version":"1.0"}`))
	if err != nil || id != "rel-1" {
		t.Fatalf("SpoolRelease = %q, %v", id, err)
	}
	recs, err := spool.ReadRecords(filepath.Join(dir, spool.DefaultFileName))
	if err != nil || len(recs) != 1 {
		t.Fatalf("records = %v, %v", recs, err)
	}
	r := recs[0]
	body, _ := base64.StdEncoding.DecodeString(r.BodyBase64)
	if r.Kind != RecordKindRelease || r.ProjectID != 42 || r.IngestID != "rel-1" || string(body) != `{"version":"1.0"}` {
		t.Fatalf("record = %+v body %q", r, body)
	}
}

func TestSpoolReleaseFailsWithoutASpoolOrWhenTheSpoolIsFull(t *testing.T) {
	t.Parallel()
	var nilHandler *Handler
	if _, err := nilHandler.SpoolRelease(1, "", "", nil); err == nil {
		t.Error("a nil handler cannot spool")
	}
	if _, err := (&Handler{}).SpoolRelease(1, "", "", nil); err == nil {
		t.Error("a handler without a spool cannot spool")
	}
	full, err := spool.NewWithLimit(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	h := NewHandler(auth.New("k"), full, 0)
	if _, err := h.SpoolRelease(1, "application/json", "", []byte("{}")); !errors.Is(err, spool.ErrFull) {
		t.Fatalf("SpoolRelease on a full spool = %v, want ErrFull", err)
	}
}

func TestTrackEventIngestedIsThrottledPerProject(t *testing.T) {
	t.Parallel()
	h := NewHandler(auth.New("k"), mustSpool(t), 0)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }

	h.trackEventIngested(0, "full", "skip")
	if _, ok := h.funnelbarnLastFired.Load(int64(0)); ok {
		t.Fatal("a request without a project must not be tracked")
	}

	h.trackEventIngested(7, "ingest", "demo")
	first, ok := h.funnelbarnLastFired.Load(int64(7))
	if !ok || !first.(time.Time).Equal(now) {
		t.Fatalf("first sighting not recorded: %v %v", first, ok)
	}

	now = now.Add(funnelbarnEventThrottle - time.Second)
	h.trackEventIngested(7, "ingest", "demo")
	if got, _ := h.funnelbarnLastFired.Load(int64(7)); !got.(time.Time).Equal(first.(time.Time)) {
		t.Fatal("a second call inside the window must not fire again")
	}

	now = now.Add(2 * time.Second)
	h.trackEventIngested(7, "ingest", "demo")
	if got, _ := h.funnelbarnLastFired.Load(int64(7)); !got.(time.Time).Equal(now) {
		t.Fatal("a call after the window must fire again")
	}
}

func TestGenerateIngestIDIsUnique(t *testing.T) {
	t.Parallel()
	a, b := generateIngestID(), generateIngestID()
	if a == b || len(a) != 24 {
		t.Fatalf("ids %q and %q", a, b)
	}
}

func TestServeHTTPUnwiredHandlerAnswersUnavailable(t *testing.T) {
	t.Parallel()
	var h *Handler
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestServeAlertmanagerHTTPRejections(t *testing.T) {
	t.Parallel()
	h := NewHandler(auth.New("secret"), mustSpool(t), 1024)
	post := func(key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/alertmanager", strings.NewReader(body))
		if key != "" {
			req.Header.Set(auth.HeaderAPIKey, key)
		}
		rr := httptest.NewRecorder()
		h.ServeAlertmanagerHTTP(rr, req)
		return rr
	}

	if rr := post("wrong", `{"alerts":[]}`); rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: status = %d, want 401", rr.Code)
	}
	if rr := post("secret", `{not json`); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed envelope: status = %d, want 400", rr.Code)
	}
	if rr := post("secret", `{"status":"firing"}`); rr.Code != http.StatusBadRequest {
		t.Errorf("envelope without alerts: status = %d, want 400", rr.Code)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v1/alertmanager", nil)
	rr := httptest.NewRecorder()
	h.ServeAlertmanagerHTTP(rr, get)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET: status = %d allow = %q", rr.Code, rr.Header().Get("Allow"))
	}

	big := strings.Repeat("x", 2048)
	if rr := post("secret", big); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: status = %d, want 413", rr.Code)
	}
}

func TestServeAlertmanagerHTTPReturnsBackpressureWhenTheSpoolIsFull(t *testing.T) {
	t.Parallel()
	full, err := spool.NewWithLimit(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	h := NewHandler(auth.New("secret"), full, 1024)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alertmanager", strings.NewReader(`{"alerts":[{"labels":{"alertname":"A"}}]}`))
	req.Header.Set(auth.HeaderAPIKey, "secret")
	rr := httptest.NewRecorder()
	h.ServeAlertmanagerHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rr.Code)
	}
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestServeHTTPAnswersMalformedWhenTheBodyCannotBeRead(t *testing.T) {
	t.Parallel()
	h := NewHandler(auth.New("secret"), mustSpool(t), 1024)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", failingBody{})
	req.Header.Set(auth.HeaderAPIKey, "secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}
