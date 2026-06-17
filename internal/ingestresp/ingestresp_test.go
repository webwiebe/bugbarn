package ingestresp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteAccepted(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteAccepted(rr, "abc123")

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rr.Code)
	}
	var body Accepted
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Accepted {
		t.Fatalf("accepted = false, want true")
	}
	if body.IngestID != "abc123" {
		t.Fatalf("ingestId = %q, want abc123", body.IngestID)
	}
}

func TestWriteAcceptedOmitsEmptyIngestID(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteAccepted(rr, "")
	if got := rr.Body.String(); got != `{"accepted":true}`+"\n" {
		t.Fatalf("body = %q, want accepted-only object", got)
	}
}

func TestWriteDropped(t *testing.T) {
	cases := []struct {
		name            string
		drop            Drop
		wantStatus      int
		wantRetryable   bool
		wantRetryHeader bool
	}{
		{"malformed", DropMalformed, http.StatusBadRequest, false, false},
		{"unauthorized", DropUnauthorized, http.StatusUnauthorized, false, false},
		{"too large", DropTooLarge, http.StatusRequestEntityTooLarge, false, false},
		{"spool full", DropSpoolFull, http.StatusTooManyRequests, true, true},
		{"unavailable", DropUnavailable, http.StatusServiceUnavailable, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			WriteDropped(rr, c.drop)

			if rr.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, c.wantStatus)
			}
			var body Dropped
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Accepted {
				t.Fatalf("accepted = true, want false for a drop")
			}
			if body.Reason != c.drop.Reason {
				t.Fatalf("reason = %q, want %q", body.Reason, c.drop.Reason)
			}
			if body.Retryable != c.wantRetryable {
				t.Fatalf("retryable = %v, want %v", body.Retryable, c.wantRetryable)
			}
			gotHeader := rr.Header().Get("Retry-After") != ""
			if gotHeader != c.wantRetryHeader {
				t.Fatalf("Retry-After present = %v, want %v", gotHeader, c.wantRetryHeader)
			}
		})
	}
}

// TestDropContract pins the permanent-vs-retryable split: 4xx-class client
// errors must never be retryable, and transient drops must be.
func TestDropContract(t *testing.T) {
	permanent := []Drop{DropMalformed, DropUnauthorized, DropTooLarge}
	for _, d := range permanent {
		if d.Retryable {
			t.Errorf("%q is a permanent drop but marked retryable", d.Reason)
		}
		if d.Status < 400 || d.Status >= 500 {
			t.Errorf("%q permanent drop has non-4xx status %d", d.Reason, d.Status)
		}
	}
	for _, d := range []Drop{DropSpoolFull, DropUnavailable} {
		if !d.Retryable {
			t.Errorf("%q is a transient drop but not marked retryable", d.Reason)
		}
	}
}
