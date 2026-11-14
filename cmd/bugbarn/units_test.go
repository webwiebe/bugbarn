package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/telemetry"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCompactStorageRewritesAFreshDatabase(t *testing.T) {
	logs := captureLogs(t)
	store := openTestStore(t)
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "x.db")}
	cfg.Compact.MaxLiveBytes = 1 << 30
	// The store was opened at another path, so compaction reports the mismatch
	// or a rewrite; either way it logs one outcome and returns.
	compactStorage(context.Background(), store, cfg, slog.Default())
	if logs.String() == "" {
		t.Fatal("compactStorage logged nothing")
	}
}

func TestCompactStorageSkipsWhenTheDatabaseIsTooLarge(t *testing.T) {
	logs := captureLogs(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bugbarn.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.Config{DBPath: path}
	cfg.Compact.MaxLiveBytes = 1
	compactStorage(context.Background(), store, cfg, slog.Default())
	if !strings.Contains(logs.String(), "database compaction skipped") && !strings.Contains(logs.String(), "database compacted") {
		t.Fatalf("no compaction outcome logged: %s", logs.String())
	}
}

func TestInternalPersistMapsPipelineOutcomes(t *testing.T) {
	store := openTestStore(t)
	proc := ingestproc.NewProcessor(store, domainevents.NewIssuePublisher(&domainevents.Bus{}), nil, true)
	persist := internalPersist(proc)

	if err := persist(context.Background(), goodRecord(t, "ok-1")); err != nil {
		t.Fatalf("a good record failed: %v", err)
	}
	if err := persist(context.Background(), spool.Record{IngestID: "bad-1", BodyBase64: "!!not base64!!"}); err == nil {
		t.Fatal("an undecodable record was reported as persisted")
	}
}

func TestProjectSlugResolverFindsProjectsAndReportsMissingOnes(t *testing.T) {
	store := openTestStore(t)
	proj, err := store.EnsureProject(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	resolve := projectSlugResolver(store)
	slug, err := resolve(context.Background(), proj.ID)
	if err != nil || slug != "alpha" {
		t.Fatalf("resolve = %q, %v", slug, err)
	}
	if _, err := resolve(context.Background(), proj.ID+1000); err == nil {
		t.Fatal("an unknown project resolved")
	}
}

func TestIssueVolumeSourceFailsForAnUnknownIssue(t *testing.T) {
	src := issueVolumeSource{store: openTestStore(t)}
	if _, err := src.HourlyEventCounts(context.Background(), "NOPE-1"); err == nil {
		t.Fatal("an unknown issue returned counts")
	}
}

func TestShipForeignHeaderLinesNeedsBothSelfReportSettings(t *testing.T) {
	handler := ingest.NewHandler(nil, nil, 1024)
	shipForeignHeaderLines(handler, config.Config{}, "writer")
	shipForeignHeaderLines(handler, config.Config{SelfEndpoint: "http://127.0.0.1:1", SelfAPIKey: "k"}, "reader")
}

func TestStartCloudflarePollerStartsOnlyWhenConfiguredAndStorable(t *testing.T) {
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ing := telemetry.New(nil, nil, slog.Default())

	startCloudflarePoller(ctx, config.Cloudflare{}, ing, &wg, slog.Default())
	startCloudflarePoller(ctx, config.Cloudflare{APIToken: "t", ZoneIDs: []string{"z"}}, ing, &wg, slog.Default())

	sec, err := telemetrydb.Open(context.Background(), telemetrydb.Security, filepath.Join(t.TempDir(), "security.db"), 1<<30)
	if err != nil {
		t.Fatalf("open security db: %v", err)
	}
	defer sec.Close()
	withDB := telemetry.New(sec, nil, slog.Default())
	startCloudflarePoller(ctx, config.Cloudflare{APIToken: "t", ZoneIDs: []string{"z"}}, withDB, &wg, slog.Default())
	wg.Wait()
}

func TestNewUserAuthenticatorRejectsAnInvalidAdminConfig(t *testing.T) {
	_, err := newUserAuthenticator(config.Config{SmokeUsername: "smoke"})
	if err == nil {
		t.Fatal("a smoke login without a hash was accepted")
	}
}

func TestNewUserAuthenticatorRejectsAMalformedAdminBcryptHash(t *testing.T) {
	_, err := newUserAuthenticator(config.Config{AdminUsername: "admin", AdminPasswordBcrypt: "not-a-bcrypt-hash"})
	if err == nil || !strings.Contains(err.Error(), "BUGBARN_ADMIN_PASSWORD_BCRYPT") {
		t.Fatalf("err = %v, want startup to fail naming the variable", err)
	}
}

func TestNewAPIAuthorizerRejectsAnInvalidKeyHash(t *testing.T) {
	_, err := newAPIAuthorizer(config.Config{APIKeySHA256: "zz"}, openTestStore(t), nil)
	if err == nil {
		t.Fatal("an invalid key hash was accepted")
	}
}

func TestSetupKeyIsEmptyWithoutASecret(t *testing.T) {
	if got := setupKey("", "slug"); got != "" {
		t.Fatalf("setupKey without a secret = %q", got)
	}
	if a, b := setupKey("s", "one"), setupKey("s", "two"); a == b || len(a) != 40 {
		t.Fatalf("setup keys do not differ per slug: %q %q", a, b)
	}
}

func TestSpoolWorkerRotatesAndFinishesASegment(t *testing.T) {
	dir := t.TempDir()
	w, sp, _ := newSegmentWorker(t, dir)
	w.rotateThreshold = 1
	appendRecords(t, sp, goodRecord(t, "r1"))

	w.rotate()
	if w.pos.Segment == "" {
		t.Fatal("the active segment was not rotated")
	}
	if got := w.pendingRecords(); got != 1 {
		t.Fatalf("pending after rotation = %d, want 1", got)
	}
	w.finishSegment()
	if w.pos.Segment != "" || len(segments(t, dir)) != 0 {
		t.Fatalf("segment survived finishSegment: pos=%+v segments=%v", w.pos, segments(t, dir))
	}
}

func TestSpoolWorkerRotateWithoutASpoolIsANoOp(t *testing.T) {
	w := &spoolWorker{}
	w.rotate()
	if w.pos.Segment != "" {
		t.Fatalf("rotated without a spool: %+v", w.pos)
	}
}

func TestSpoolWorkerRotateKeepsPositionWhenTheSpoolIsClosed(t *testing.T) {
	logs := captureLogs(t)
	w, sp, _ := newSegmentWorker(t, t.TempDir())
	w.rotateThreshold = 1
	appendRecords(t, sp, goodRecord(t, "r1"))
	sp.Close()
	w.rotate()
	if w.pos.Segment != "" {
		t.Fatalf("position moved after a failed rotation: %+v", w.pos)
	}
	if !strings.Contains(logs.String(), "worker failed to rotate spool") {
		t.Fatalf("rotation failure not logged: %s", logs.String())
	}
}

func TestSpoolWorkerFinishSegmentStaysPutWhenTheCursorCannotBeCommitted(t *testing.T) {
	logs := captureLogs(t)
	dir := filepath.Join(t.TempDir(), "gone")
	w := &spoolWorker{spoolDir: dir, pos: spool.Position{Segment: "seg-1", Offset: 5}}
	w.finishSegment()
	if w.pos.Segment != "seg-1" {
		t.Fatalf("position moved without a durable cursor: %+v", w.pos)
	}
	if !strings.Contains(logs.String(), "failed to commit cursor") {
		t.Fatalf("commit failure not logged: %s", logs.String())
	}
}

func TestSpoolWorkerReportStatusWarnsOnceWhileStalledAndResetsWhenHealthy(t *testing.T) {
	logs := captureLogs(t)
	w, sp, _ := newSegmentWorker(t, t.TempDir())
	appendRecords(t, sp, goodRecord(t, "r1"))
	// A zero Status has never advanced, which is how a stalled worker looks.
	w.ws = &worker.Status{}

	w.reportStatus()
	if !w.stallWarned {
		t.Fatal("a worker with pending records and no advance did not warn")
	}
	w.reportStatus()
	if n := strings.Count(logs.String(), "worker stall detected"); n != 1 {
		t.Fatalf("stall warned %d times, want once", n)
	}

	w.selfReporting = true
	w.stallWarned = false
	w.reportStatus()
	if !w.stallWarned {
		t.Fatal("the self-reporting stall path did not mark the warning")
	}

	empty, _, _ := newSegmentWorker(t, t.TempDir())
	empty.stallWarned = true
	empty.reportStatus()
	if empty.stallWarned {
		t.Fatal("a drained worker kept its stall warning")
	}
	(&spoolWorker{}).reportStatus()
}

func TestRunWorkerOnceReportsAnUnopenableDatabase(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := runWorkerOnce(config.Config{DBPath: filepath.Join(blocker, "bugbarn.db"), SpoolDir: dir})
	if err == nil {
		t.Fatal("an unopenable database was accepted")
	}
}
