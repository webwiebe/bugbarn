package worker

import (
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func TestProcessRecordNormalizesAndFingerprints(t *testing.T) {
	raw, err := os.ReadFile("../../specs/001-personal-error-tracker/fixtures/example-event.json")
	if err != nil {
		t.Fatal(err)
	}

	processed, err := ProcessRecord(spool.Record{
		IngestID:   "ing-1",
		ReceivedAt: time.Date(2026, 4, 15, 8, 31, 0, 0, time.UTC),
		BodyBase64: base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed.Event.Exception.Type != "TypeError" {
		t.Fatalf("unexpected event: %#v", processed.Event)
	}
	if processed.Fingerprint == "" {
		t.Fatal("expected fingerprint")
	}
}

func TestProcessRecordsStopsOnInvalidRecord(t *testing.T) {
	_, err := ProcessRecords([]spool.Record{
		{IngestID: "bad", BodyBase64: "not-base64"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
