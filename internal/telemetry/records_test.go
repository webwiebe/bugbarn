package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

var cfRecord = secnorm.Record{TS: day.Add(time.Hour), Source: "cloudflare", Kind: "waf", SrcIP: "203.0.113.7", Action: "block"}

func TestIngestRecordsObservesStoresAndKeepsCursor(t *testing.T) {
	ctx := context.Background()
	sec := openDB(t, telemetrydb.Security)
	ing := New(sec, nil, quiet())
	obs := &recordingObserver{}
	ing.SetObserver(obs)

	if err := ing.IngestRecords(ctx, []secnorm.Record{cfRecord}, "cloudflare.cursor.z", "c1"); err != nil {
		t.Fatal(err)
	}
	if len(obs.security) != 1 {
		t.Fatalf("observed %d", len(obs.security))
	}
	rows, err := sec.SearchSecurity(ctx, telemetrydb.SecurityQuery{From: day, To: day.Add(24 * time.Hour)})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v, err %v", rows, err)
	}
	if v, err := ing.Cursor(ctx, "cloudflare.cursor.z"); err != nil || v != "c1" {
		t.Fatalf("cursor %q, err %v", v, err)
	}
}

// At the size cap the cursor still advances and detections still run.
func TestIngestRecordsFullFileAdvancesCursor(t *testing.T) {
	ctx := context.Background()
	ing := New(openFull(t), nil, quiet())
	obs := &recordingObserver{}
	ing.SetObserver(obs)

	if err := ing.IngestRecords(ctx, []secnorm.Record{cfRecord}, "k", "c2"); err != nil {
		t.Fatalf("err = %v, want nil for a full file", err)
	}
	if len(obs.security) != 1 {
		t.Fatalf("observed %d", len(obs.security))
	}
	if v, _ := ing.Cursor(ctx, "k"); v != "c2" {
		t.Fatalf("cursor %q", v)
	}
}

func TestIngestRecordsWithoutSecurityFile(t *testing.T) {
	ing := New(nil, nil, quiet())
	if err := ing.IngestRecords(context.Background(), nil, "k", "v"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := ing.Cursor(context.Background(), "k"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
