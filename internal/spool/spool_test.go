package spool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendWritesNDJSON(t *testing.T) {
	dir := t.TempDir()

	eventSpool, err := New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer eventSpool.Close()

	record := Record{
		IngestID:    "abc123",
		ReceivedAt:  time.Unix(123, 0).UTC(),
		ContentType: "application/json",
		RemoteAddr:  "127.0.0.1:4321",
		BodyBase64:  "eyJmb28iOiJiYXIifQ==",
	}

	if err := eventSpool.Append(record); err != nil {
		t.Fatalf("append: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, DefaultFileName))
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}

	var got Record
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode record: %v", err)
	}

	if got.IngestID != record.IngestID {
		t.Fatalf("expected ingest id %q, got %q", record.IngestID, got.IngestID)
	}

	if got.BodyBase64 != record.BodyBase64 {
		t.Fatalf("expected body %q, got %q", record.BodyBase64, got.BodyBase64)
	}
}

func TestReadRecordsReturnsAppendedRecords(t *testing.T) {
	dir := t.TempDir()
	eventSpool, err := New(dir)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	defer eventSpool.Close()

	first := Record{IngestID: "first", BodyBase64: "e30="}
	second := Record{IngestID: "second", BodyBase64: "e30="}
	if err := eventSpool.Append(first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := eventSpool.Append(second); err != nil {
		t.Fatalf("append second: %v", err)
	}

	records, err := ReadRecords(Path(dir))
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("unexpected record count: got %d want %d", got, want)
	}
	if records[0].IngestID != "first" || records[1].IngestID != "second" {
		t.Fatalf("unexpected records: %#v", records)
	}
}
