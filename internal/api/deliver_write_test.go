package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// deliverWrite picks where an ingest write goes: the spool when there is one,
// else the writer, else the local handler (#271, H1).
func TestDeliverWriteOrder(t *testing.T) {
	t.Parallel()

	type result struct {
		local  bool
		writer bool
		code   int
	}
	run := func(t *testing.T, withSpool, withWriter bool) result {
		t.Helper()
		var writerHits atomic.Int32
		// Other tests in the package send to closed servers (deadWriterURL), and a
		// closed server's port can be handed to this one. Count only requests that
		// carry this test's marker, so a stray request never reads as a writer hit.
		const marker = "X-Deliver-Write-Test"
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(marker) != "" {
				writerHits.Add(1)
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		t.Cleanup(upstream.Close)

		store := mustOpenStore(t)
		t.Cleanup(func() { _ = store.Close() })
		server := NewServer(nil, store, nil)
		if withSpool {
			server.SetIngestSpool(newTestSpool(t, "http://localhost:0"))
		}
		if withWriter {
			server.SetWriteForwarder(NewWriteForwarder(upstream.URL))
		}

		local := false
		req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader("not json"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(marker, "1")
		rec := httptest.NewRecorder()
		server.deliverWrite(rec, req, func(w http.ResponseWriter, _ *http.Request) {
			local = true
			w.WriteHeader(http.StatusTeapot)
		})
		return result{local: local, writer: writerHits.Load() > 0, code: rec.Code}
	}

	t.Run("spool wins over the writer forwarder", func(t *testing.T) {
		t.Parallel()
		// The spool refuses the malformed body with a 400; only the spool does that.
		got := run(t, true, true)
		if got.local || got.writer || got.code != http.StatusBadRequest {
			t.Fatalf("got %+v, want the spool to answer 400 and nothing else to run", got)
		}
	})
	t.Run("writer forwarder when there is no spool", func(t *testing.T) {
		t.Parallel()
		got := run(t, false, true)
		if got.local || !got.writer || got.code != http.StatusAccepted {
			t.Fatalf("got %+v, want the writer to answer 202", got)
		}
	})
	t.Run("local handler on the writer", func(t *testing.T) {
		t.Parallel()
		got := run(t, false, false)
		if !got.local || got.writer || got.code != http.StatusTeapot {
			t.Fatalf("got %+v, want the local handler to answer 418", got)
		}
	})
}
