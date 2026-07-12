package sessionstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// The reader replicas call writer-internal HTTP endpoints for every session
// mutation. Requests are authenticated by an HMAC-SHA256 over the exact JSON
// body, keyed with the shared BUGBARN_SESSION_SECRET, plus a timestamp inside
// the signed body to bound replay.

// AuthHeader carries the request-body HMAC on writer-internal session calls.
const AuthHeader = "X-BugBarn-Session-Auth"

// InternalPathPrefix is the writer-internal session endpoint namespace.
const InternalPathPrefix = "/internal/v1/sessions/"

// RequestMaxSkew bounds how stale a signed internal request's timestamp may
// be before it is rejected as a replay.
const RequestMaxSkew = 5 * time.Minute

// Request is the signed body of a writer-internal session call. TS is the
// sender's unix time; exactly one of the operation fields is set.
type Request struct {
	TS      int64               `json:"ts"`
	IDHash  string              `json:"id_hash,omitempty"`
	SID     string              `json:"sid,omitempty"`
	Sub     string              `json:"sub,omitempty"`
	Session *storage.WebSession `json:"session,omitempty"`
}

// Response is the writer's answer. Status "ok" carries a current session;
// "transient" carries the stale session during an IdP outage (bounded grace
// applies on the caller side).
type Response struct {
	Status  string              `json:"status,omitempty"`
	Error   string              `json:"error,omitempty"`
	Session *storage.WebSession `json:"session,omitempty"`
	Deleted int64               `json:"deleted,omitempty"`
}

// Response status values.
const (
	StatusOK        = "ok"
	StatusTransient = "transient"
)

// SignBody returns the auth header value for a request body.
func SignBody(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "v1:" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyBody checks a request body against the auth header value in constant
// time.
func VerifyBody(secret, body []byte, header string) bool {
	header = strings.TrimSpace(header)
	if header == "" || len(secret) == 0 {
		return false
	}
	expected := SignBody(secret, body)
	return hmac.Equal([]byte(header), []byte(expected))
}

// FreshTS reports whether a signed timestamp is within the replay window.
func FreshTS(ts int64, now time.Time) bool {
	if ts == 0 {
		return false
	}
	delta := now.Sub(time.Unix(ts, 0))
	if delta < 0 {
		delta = -delta
	}
	return delta <= RequestMaxSkew
}
