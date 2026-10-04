package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

const (
	sshLine    = `{"source":"syslog","host":"mini1","timestamp":"2026-10-04T11:59:00Z","SYSLOG_IDENTIFIER":"sshd","message":"Failed password for root from 203.0.113.9 port 4022 ssh2"}`
	metricLine = `{"name":"load1","tags":{"host":"mini1"},"timestamp":"2026-10-04T11:59:00Z","gauge":{"value":1.5}}`
)

var day = time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func openDB(t *testing.T, spec telemetrydb.Spec) *telemetrydb.DB {
	t.Helper()
	d, err := telemetrydb.Open(context.Background(), spec, filepath.Join(t.TempDir(), spec.Name+".db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

type recordingObserver struct {
	security []secnorm.Record
	samples  []hostmetrics.Sample
	hosts    []hostmetrics.HostInfo
}

func (o *recordingObserver) ObserveSecurity(_ context.Context, recs []secnorm.Record) {
	o.security = append(o.security, recs...)
}

func (o *recordingObserver) ObserveMetrics(_ context.Context, s []hostmetrics.Sample, h []hostmetrics.HostInfo) {
	o.samples = append(o.samples, s...)
	o.hosts = append(o.hosts, h...)
}

func TestIngestSecurityObservesAndStores(t *testing.T) {
	ctx := context.Background()
	sec := openDB(t, telemetrydb.Security)
	ing := New(sec, nil, quiet())
	obs := &recordingObserver{}
	ing.SetObserver(obs)

	if err := ing.Ingest(ctx, KindSecurity, []byte(sshLine+"\nnot json\n")); err != nil {
		t.Fatal(err)
	}
	if len(obs.security) != 1 || obs.security[0].SrcIP != "203.0.113.9" {
		t.Fatalf("observed %+v", obs.security)
	}
	rows, err := sec.SearchSecurity(ctx, telemetrydb.SecurityQuery{From: day, To: day.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Kind != "auth_failure" {
		t.Fatalf("stored %+v", rows)
	}
}

func TestIngestMetricsObservesAndStores(t *testing.T) {
	ctx := context.Background()
	met := openDB(t, telemetrydb.Metrics)
	ing := New(nil, met, quiet())
	obs := &recordingObserver{}
	ing.SetObserver(obs)

	if err := ing.Ingest(ctx, KindMetrics, []byte(metricLine)); err != nil {
		t.Fatal(err)
	}
	if len(obs.samples) != 1 || len(obs.hosts) != 1 {
		t.Fatalf("observed %d samples, %d hosts", len(obs.samples), len(obs.hosts))
	}
	series, err := met.Series(ctx, "mini1", "load1", day, day.Add(24*time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].Value != 1.5 {
		t.Fatalf("series %+v", series)
	}
}

// A full file still runs detections and reports success: the raw rows are
// best-effort and a retry would make detections count the batch twice.
func TestIngestFullFileStillObserves(t *testing.T) {
	ctx := context.Background()
	ing := New(openFull(t), nil, quiet())
	obs := &recordingObserver{}
	ing.SetObserver(obs)
	if err := ing.Ingest(ctx, KindSecurity, []byte(sshLine)); err != nil {
		t.Fatalf("err = %v, want nil for a full file", err)
	}
	if len(obs.security) != 1 {
		t.Fatalf("observer saw %d records, want 1", len(obs.security))
	}
}

// openFull returns a security file whose cap is already exceeded, so
// EnforceCap marks it full.
func openFull(t *testing.T) *telemetrydb.DB {
	t.Helper()
	d, err := telemetrydb.Open(context.Background(), telemetrydb.Security, filepath.Join(t.TempDir(), "full.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.EnforceCap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !d.Full() {
		t.Fatal("a 1-byte cap did not mark the file full")
	}
	return d
}

func TestIngestErrors(t *testing.T) {
	ctx := context.Background()
	ing := New(nil, nil, quiet())
	if err := ing.Ingest(ctx, KindSecurity, []byte(sshLine)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("security without a file: %v", err)
	}
	if err := ing.Ingest(ctx, KindMetrics, []byte(metricLine)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("metrics without a file: %v", err)
	}
	if err := ing.Ingest(ctx, "event", nil); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind: %v", err)
	}
	// A batch with nothing usable is not an error.
	sec := openDB(t, telemetrydb.Security)
	if err := New(sec, nil, quiet()).Ingest(ctx, KindSecurity, []byte("garbage")); err != nil {
		t.Fatalf("unparseable batch: %v", err)
	}
}
