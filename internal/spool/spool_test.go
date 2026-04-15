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
