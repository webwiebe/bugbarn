package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// When an ingest spool is wired, POST /api/v1/releases must enqueue the marker
// and return 202 instead of writing it synchronously — keeping the request off
// the single SQLite writer connection that the background worker holds.
func TestCreateReleaseFireAndForget(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	eventSpool, err := spool.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ingestHandler := ingest.NewHandler(auth.New(""), eventSpool, 1<<20)
	server := NewServer(ingestHandler, store, nil)

	payload := `{"name":"v2.0.0","environment":"production","version":"2.0.0","commitSha":"deadbeef","url":"https://example.com/r","notes":"async deploy"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("release create status: got %d want %d body=%q", rr.Code, http.StatusAccepted, rr.Body.String())
	}
	var resp struct {
		Accepted bool   `json:"accepted"`
		IngestID string `json:"ingestId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Accepted || resp.IngestID == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Nothing should have been written synchronously.
	releases, err := store.ListReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Fatalf("expected no synchronous release write, got %d", len(releases))
	}

	// The marker must be on the spool as a release record.
	records, err := spool.ReadRecords(eventSpool.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 spooled record, got %d", len(records))
	}
	if records[0].Kind != ingest.RecordKindRelease {
		t.Fatalf("spooled record kind: got %q want %q", records[0].Kind, ingest.RecordKindRelease)
	}
	body, err := base64.StdEncoding.DecodeString(records[0].BodyBase64)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "v2.0.0" {
		t.Fatalf("spooled release name: got %q want %q", got.Name, "v2.0.0")
	}
}
