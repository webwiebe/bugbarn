package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// fakePersister stands in for the ingestproc pipeline (which this package
// cannot import). It classifies a record the way the pipeline does for the
// cases the worker's delivery policy cares about.
type fakePersister struct {
	mu        sync.Mutex
	persisted []string
	releases  []string
	// heldSlugs are projects pending approval: their events are held.
	heldSlugs map[string]bool
	// failPersist makes every otherwise good event fail with a permanent error.
	failPersist bool
}

func (f *fakePersister) PersistRecord(ctx context.Context, record spool.Record) PersistResult {
	if err := ctx.Err(); err != nil {
		return PersistResult{Outcome: OutcomeTransient, Err: err}
	}
	if _, err := base64.StdEncoding.DecodeString(record.BodyBase64); err != nil {
		return PersistResult{Outcome: OutcomeParseError, Err: err}
	}
	if f.failPersist {
		return PersistResult{Outcome: OutcomePersistError, Err: errors.New("database closed")}
	}
	if f.heldSlugs[record.ProjectSlug] {
		return PersistResult{Outcome: OutcomeHeld}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persisted = append(f.persisted, record.IngestID)
	return PersistResult{Outcome: OutcomeSuccess, IsNew: true, Fingerprint: "fp-" + record.IngestID, Severity: "error"}
}

func (f *fakePersister) PersistRelease(_ context.Context, record spool.Record) error {
	raw, err := base64.StdEncoding.DecodeString(record.BodyBase64)
	if err == nil && !json.Valid(raw) {
		err = errors.New("release body is not JSON")
	}
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, record.IngestID)
	return nil
}

func (f *fakePersister) count() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.persisted))
}

// fakeMutator records the admin mutations the worker applies.
type fakeMutator struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (m *fakeMutator) record(op, id string) (domain.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, op+":"+id)
	return domain.Issue{ID: id}, m.err
}

func (m *fakeMutator) ResolveIssue(_ context.Context, id string) (domain.Issue, error) {
	return m.record("resolve", id)
}

func (m *fakeMutator) ReopenIssue(_ context.Context, id string) (domain.Issue, error) {
	return m.record("reopen", id)
}

func (m *fakeMutator) MuteIssue(_ context.Context, id, mode string) (domain.Issue, error) {
	return m.record("mute("+mode+")", id)
}

func (m *fakeMutator) UnmuteIssue(_ context.Context, id string) (domain.Issue, error) {
	return m.record("unmute", id)
}

// newSegmentWorker builds a worker over a real spool and mutation queue in dir
// with a fake persister, the way RunSpoolWorker wires them in the writer.
func newSegmentWorker(t *testing.T, dir string) (*spoolWorker, *spool.Spool, *fakePersister) {
	t.Helper()
	sp, err := spool.New(dir)
	if err != nil {
		t.Fatalf("open spool: %v", err)
	}
	t.Cleanup(func() { sp.Close() })
	mq, err := mutqueue.New(dir)
	if err != nil {
		t.Fatalf("open mutation queue: %v", err)
	}
	t.Cleanup(func() { mq.Close() })
	fp := &fakePersister{heldSlugs: map[string]bool{}}
	w := &spoolWorker{
		eventSpool:  sp,
		spoolDir:    dir,
		store:       &fakeMutator{},
		proc:        fp,
		ws:          NewStatus(),
		mq:          mq,
		tracer:      tracing.Tracer(),
		metrics:     newSpoolMetrics(),
		retryCounts: make(map[string]int),
		// Keep rotation out of the way unless a test asks for it.
		rotateThreshold: 1 << 40,
	}
	return w, sp, fp
}

func goodRecord(t *testing.T, id string) spool.Record {
	t.Helper()
	body := base64.StdEncoding.EncodeToString([]byte(`{"message":"boom"}`))
	return spool.Record{IngestID: id, ReceivedAt: time.Now().UTC(), BodyBase64: body}
}

func slugged(t *testing.T, id, slug string) spool.Record {
	t.Helper()
	rec := goodRecord(t, id)
	rec.ProjectSlug = slug
	return rec
}

func releaseSpoolRecord(id, body string, projectID int64) spool.Record {
	return spool.Record{
		IngestID:   id,
		Kind:       ingest.RecordKindRelease,
		ProjectID:  projectID,
		BodyBase64: base64.StdEncoding.EncodeToString([]byte(body)),
	}
}

func appendRecords(t *testing.T, sp *spool.Spool, recs ...spool.Record) {
	t.Helper()
	for _, r := range recs {
		if err := sp.Append(r); err != nil {
			t.Fatalf("append %s: %v", r.IngestID, err)
		}
	}
}

func segments(t *testing.T, dir string) []string {
	t.Helper()
	names, err := spool.ListSegments(dir)
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	return names
}

func mustPosition(t *testing.T, dir string) spool.Position {
	t.Helper()
	pos, err := spool.ReadPosition(dir)
	if err != nil {
		t.Fatalf("read position: %v", err)
	}
	return pos
}

func activeSize(t *testing.T, dir string) int64 {
	t.Helper()
	info, err := os.Stat(spool.Path(dir))
	if err != nil {
		t.Fatalf("stat active segment: %v", err)
	}
	return info.Size()
}

func cursorAtEnd(t *testing.T, w *spoolWorker) bool {
	t.Helper()
	return mustPosition(t, w.spoolDir).Offset == activeSize(t, w.spoolDir)
}

func deadLetterExists(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "deadletter.ndjson"))
	return err == nil && len(data) > 0
}

// logBuffer swaps the default slog logger for one that writes to a buffer.
// The logger is process-wide, so tests using it do not run in parallel.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureLogs(t *testing.T) *logBuffer {
	t.Helper()
	prev := slog.Default()
	lb := &logBuffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return lb
}
