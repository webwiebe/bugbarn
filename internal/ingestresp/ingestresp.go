// Package ingestresp defines the shared HTTP response contract for BugBarn's
// ingest endpoints. Every ingest response tells an SDK two things without
// requiring it to parse prose: whether the event was accepted, and — if it was
// dropped — whether retrying the identical request could ever succeed.
//
// The contract:
//
//	202   accepted   durably queued, will be processed       {"accepted":true,...}
//	4xx   dropped     permanent — never succeeds as-is, do not retry
//	429   dropped     transient backpressure — retry after Retry-After
//	503   dropped     transient outage — retry after Retry-After
//
// Accepted responses are always 2xx; dropped responses are always non-2xx and
// carry a stable machine reason plus a retryable flag. This keeps the
// accepted/dropped distinction legible from the status code alone, while the
// body lets clients act on the reason and back off correctly.
package ingestresp

import (
	"encoding/json"
	"net/http"
)

// Accepted is the body returned with a 202 when an event is durably queued.
type Accepted struct {
	Accepted bool   `json:"accepted"` // always true
	IngestID string `json:"ingestId,omitempty"`
}

// Dropped is the body returned with every non-2xx ingest response. Reason is a
// stable, machine-readable token; Retryable reports whether a later retry of
// the identical request could succeed.
type Dropped struct {
	Accepted  bool   `json:"accepted"` // always false
	Reason    string `json:"reason"`
	Retryable bool   `json:"retryable"`
}

// Drop classifies a dropped event: the HTTP status to return, a stable reason
// token, and whether the SDK should retry.
type Drop struct {
	Status    int
	Reason    string
	Retryable bool
}

// Permanent drops — the request is bad and will never succeed as-is. The SDK
// must not retry; retrying only wastes the event.
var (
	// DropMalformed: the body is not a structurally valid event payload.
	DropMalformed = Drop{Status: http.StatusBadRequest, Reason: "malformed_payload", Retryable: false}
	// DropUnauthorized: missing or invalid API key.
	DropUnauthorized = Drop{Status: http.StatusUnauthorized, Reason: "unauthorized", Retryable: false}
	// DropTooLarge: the body exceeds the ingest size limit.
	DropTooLarge = Drop{Status: http.StatusRequestEntityTooLarge, Reason: "payload_too_large", Retryable: false}
)

// Transient drops — the event was refused right now, but an identical retry may
// succeed once backpressure clears or the writer recovers.
var (
	// DropSpoolFull: the ingest buffer is full. Retry after backoff.
	DropSpoolFull = Drop{Status: http.StatusTooManyRequests, Reason: "spool_full", Retryable: true}
	// DropUnavailable: the ingest pipeline is temporarily unavailable. Retry.
	DropUnavailable = Drop{Status: http.StatusServiceUnavailable, Reason: "ingest_unavailable", Retryable: true}
)

// WriteAccepted writes a 202 with the accepted body. ingestID may be empty
// (e.g. reader pods that have not yet assigned one).
func WriteAccepted(w http.ResponseWriter, ingestID string) {
	writeJSON(w, http.StatusAccepted, Accepted{Accepted: true, IngestID: ingestID})
}

// WriteDropped writes d's status and dropped body. Retryable drops carry a
// Retry-After header so SDKs back off instead of hot-looping.
func WriteDropped(w http.ResponseWriter, d Drop) {
	if d.Retryable {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, d.Status, Dropped{Accepted: false, Reason: d.Reason, Retryable: d.Retryable})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
