package api

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

func spoolAt(t *testing.T, sf *SpoolForwarder, rec spooledRequest) {
	t.Helper()
	if rec.ReceivedAt.IsZero() {
		rec.ReceivedAt = time.Now().UTC()
	}
	if err := sf.append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestAlertmanagerRecordsExpandIntoOneQueueItemPerAlert(t *testing.T) {
	sf, q := newTestRedisForwarder(t)
	body := `{"alerts":[{"labels":{"alertname":"A"},"annotations":{"summary":"s"}},{"labels":{"alertname":"B"},"annotations":{"summary":"t"}}]}`
	spoolAt(t, sf, spooledRequest{
		Method: "POST", Path: "/api/v1/alertmanager",
		Headers:    map[string]string{projectHeader: "svc"},
		BodyBase64: base64.StdEncoding.EncodeToString([]byte(body)),
	})
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	items, err := q.Consume(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d queue items, want 2", len(items))
	}
	for _, it := range items {
		if it.Kind != queue.KindEvent || it.ProjectSlug != "svc" {
			t.Errorf("item = %+v", it)
		}
	}
}

func TestAlertmanagerRecordsThatCannotBeReadAreDropped(t *testing.T) {
	sf, q := newTestRedisForwarder(t)
	spoolAt(t, sf, spooledRequest{Method: "POST", Path: "/api/v1/alertmanager", BodyBase64: "!!not base64!!"})
	spoolAt(t, sf, spooledRequest{Method: "POST", Path: "/api/v1/alertmanager", BodyBase64: base64.StdEncoding.EncodeToString([]byte("not json"))})
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	if sf.Pending() != 0 {
		t.Fatalf("pending = %d, want the unreadable records acked", sf.Pending())
	}
	if items, _ := q.Consume(context.Background()); len(items) != 0 {
		t.Fatalf("published %d items from unreadable records", len(items))
	}
}

func TestAlertmanagerPublishFailureKeepsTheRecordForRetry(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	q, err := queue.NewRedisQueue("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	sf, err := NewRedisSpoolForwarder(t.TempDir(), q, 1<<20, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer sf.Close()
	body := `{"alerts":[{"labels":{"alertname":"A"},"annotations":{"summary":"s"}}]}`
	spoolAt(t, sf, spooledRequest{Method: "POST", Path: "/api/v1/alertmanager", BodyBase64: base64.StdEncoding.EncodeToString([]byte(body))})
	mr.Close()

	if err := sf.DrainOnce(context.Background()); err == nil {
		t.Fatal("DrainOnce succeeded with Redis down")
	}
	if sf.Pending() != 1 {
		t.Fatalf("pending = %d, want the record kept", sf.Pending())
	}
}

func TestRedisSpoolDropsRecordsWithoutAQueueKind(t *testing.T) {
	sf, _ := newTestRedisForwarder(t)
	spoolAt(t, sf, spooledRequest{Method: "POST", Path: "/api/v1/unrelated", BodyBase64: base64.StdEncoding.EncodeToString([]byte("{}"))})
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	if sf.Pending() != 0 {
		t.Fatalf("pending = %d", sf.Pending())
	}
}

func TestDrainStepForwardsRecordsAndAdvancesTheCursor(t *testing.T) {
	var got []string
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer writer.Close()
	sf := newTestSpool(t, writer.URL)
	spoolRecord(t, sf)
	spoolRecord(t, sf)

	sf.drainStep(context.Background())

	if len(got) != 2 || sf.Pending() != 0 {
		t.Fatalf("forwarded %v, pending %d", got, sf.Pending())
	}
	if off, err := readCursor(sf.dir); err != nil || off == 0 {
		t.Fatalf("cursor = %d, %v", off, err)
	}
}

func TestDrainStepKeepsTheCursorWhenTheWriterFails(t *testing.T) {
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer writer.Close()
	sf := newTestSpool(t, writer.URL)
	spoolRecord(t, sf)

	// A cancelled context skips the retry backoff; the 503 still fails the record.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sf.drainStep(ctx)

	if sf.Pending() != 1 {
		t.Fatalf("pending = %d, want the record kept", sf.Pending())
	}
	if off, _ := readCursor(sf.dir); off != 0 {
		t.Fatalf("cursor advanced to %d past a failed record", off)
	}
}

func TestDrainStepDropsRecordsTheWriterRejectsPermanently(t *testing.T) {
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer writer.Close()
	sf := newTestSpool(t, writer.URL)
	spoolRecord(t, sf)

	sf.drainStep(context.Background())

	if sf.Pending() != 0 {
		t.Fatalf("pending = %d, want the 4xx record dropped and acked", sf.Pending())
	}
}

func TestDrainStepSurvivesACorruptCursor(t *testing.T) {
	sf := newTestSpool(t, "http://127.0.0.1:1")
	if err := os.WriteFile(filepath.Join(sf.dir, spoolCursorFileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	sf.drainStep(cancelled())
	if _, err := readCursor(sf.dir); err == nil {
		t.Fatal("a corrupt cursor was read without error")
	}
	if err := sf.DrainOnce(context.Background()); err == nil {
		t.Fatal("DrainOnce ignored a corrupt cursor")
	}
}

func TestDrainStepRotatesAnOversizedDrainedSpool(t *testing.T) {
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer writer.Close()
	sf := newTestSpool(t, writer.URL)
	sf.rotateBytes = 1
	spoolRecord(t, sf)
	sf.drainStep(context.Background())
	// The spool is drained and oversized: the next pass finds nothing to
	// forward and starts a fresh file with a reset cursor.
	sf.drainStep(cancelled())

	if info, err := os.Stat(sf.path); err != nil || info.Size() != 0 {
		t.Fatalf("spool file after rotation: %v, %v", info, err)
	}
	if off, _ := readCursor(sf.dir); off != 0 {
		t.Fatalf("cursor after rotation = %d", off)
	}
	spoolRecord(t, sf)
	if sf.Pending() != 1 {
		t.Fatalf("pending after rotation = %d", sf.Pending())
	}
}

func TestDrainStopsWhenItsContextEnds(t *testing.T) {
	sf := newTestSpool(t, "http://127.0.0.1:1")
	sf.Drain(cancelled())
}

func TestForwardOneDropsACorruptRecord(t *testing.T) {
	sf := newTestSpool(t, "http://127.0.0.1:1")
	spoolAt(t, sf, spooledRequest{Method: "POST", Path: "/api/v1/events", BodyBase64: "!!not base64!!"})
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("a corrupt record blocked the spool: %v", err)
	}
	if sf.Pending() != 0 {
		t.Fatalf("pending = %d", sf.Pending())
	}
}

func TestReadRecordsSkipsCorruptLinesAndResetsAStaleOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.ndjson")
	good := `{"receivedAt":"2026-01-01T00:00:00Z","method":"POST","path":"/api/v1/events","bodyBase64":""}`
	if err := os.WriteFile(path, []byte("{truncated\n\n"+good+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, end, err := readRecords(path, 1<<20)
	if err != nil || len(recs) != 1 || end == 0 {
		t.Fatalf("readRecords = %d records, end %d, %v", len(recs), end, err)
	}
	if recs, off, err := readRecords(filepath.Join(t.TempDir(), "absent"), 7); err != nil || recs != nil || off != 7 {
		t.Fatalf("missing file = %v, %d, %v", recs, off, err)
	}
	if _, _, err := readRecords(t.TempDir(), 0); err == nil {
		t.Fatal("a directory read as a spool file")
	}
}

func TestCursorHelpers(t *testing.T) {
	dir := t.TempDir()
	if off, err := readCursor(dir); err != nil || off != 0 {
		t.Fatalf("empty cursor = %d, %v", off, err)
	}
	if err := writeCursor(dir, 42); err != nil {
		t.Fatal(err)
	}
	if off, _ := readCursor(dir); off != 42 {
		t.Fatalf("cursor = %d", off)
	}
	if err := resetCursor(dir); err != nil {
		t.Fatal(err)
	}
	if err := resetCursor(dir); err != nil {
		t.Fatalf("resetting an absent cursor: %v", err)
	}
	if err := writeCursor(filepath.Join(dir, "missing"), 1); err == nil {
		t.Fatal("wrote a cursor into a missing directory")
	}
}

func TestNewSpoolForwardersValidateTheirArguments(t *testing.T) {
	if _, err := NewSpoolForwarder("", "http://w", 1, slog.Default()); err == nil {
		t.Error("an empty dir was accepted")
	}
	if _, err := NewSpoolForwarder(t.TempDir(), "", 1, slog.Default()); err == nil {
		t.Error("an empty writer url was accepted")
	}
	if _, err := NewRedisSpoolForwarder("", nil, 1, slog.Default()); err == nil {
		t.Error("an empty dir was accepted for the redis forwarder")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSpoolForwarder(filepath.Join(blocker, "sub"), "http://w", 1, slog.Default()); err == nil {
		t.Error("a spool dir under a file was accepted")
	}
}

func TestForwardRejectsAnOversizedBodyAndALogWithoutAProject(t *testing.T) {
	sf, _ := newTestRedisForwarder(t)
	sf.maxBodyByte = 8
	req := httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(`{"message":"far too long for the limit"}`))
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d", rec.Code)
	}

	sf.maxBodyByte = 1 << 20
	req = httptest.NewRequest("POST", "/api/v1/logs", strings.NewReader(`{"logs":[{"message":"x"}]}`))
	rec = httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("log without a project status = %d, want 400", rec.Code)
	}
}
