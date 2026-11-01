package bugbarn

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// dropEvery limits how often one reason reaches stderr.
const dropEvery = 10 * time.Minute

// responseError is an ingest answer outside 2xx. The reasons and the split
// between permanent and retryable follow internal/ingestresp on the server.
// The SDK has no retry, so the event is lost in both cases.
type responseError struct {
	status int
}

func checkResponse(status int) error {
	if status >= 200 && status < 300 {
		return nil
	}
	return &responseError{status: status}
}

func (e *responseError) Error() string {
	kind := "permanent"
	if e.retryable() {
		kind = "retryable"
	}
	return fmt.Sprintf("HTTP %d %s, %s", e.status, e.reason(), kind)
}

func (e *responseError) retryable() bool {
	return e.status == http.StatusTooManyRequests || e.status >= 500
}

func (e *responseError) reason() string {
	switch e.status {
	case http.StatusBadRequest:
		return "malformed_payload"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusTooManyRequests:
		return "spool_full"
	case http.StatusServiceUnavailable:
		return "ingest_unavailable"
	}
	return fmt.Sprintf("status_%d", e.status)
}

// dropLog makes lost deliveries visible on stderr, one line per reason per
// interval. It writes to stderr directly because BugBarn's selflog hands slog
// errors to this SDK, so reporting through slog would loop.
type dropLog struct {
	out   io.Writer
	every time.Duration
	now   func() time.Time

	mu   sync.Mutex
	seen map[string]*dropCount
}

type dropCount struct {
	last  time.Time
	count int
}

func newDropLog(out io.Writer) *dropLog {
	return &dropLog{out: out, every: dropEvery, now: time.Now, seen: make(map[string]*dropCount)}
}

func (d *dropLog) record(what, endpoint string, err error) {
	reason, detail := describeDrop(err)
	key := what + ":" + reason

	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.seen[key]
	if c == nil {
		c = &dropCount{}
		d.seen[key] = c
	}
	c.count++
	now := d.now()
	if !c.last.IsZero() && now.Sub(c.last) < d.every {
		return
	}
	_, _ = fmt.Fprintf(d.out, "bugbarn: %d %s dropped since the last line, %s (%s)\n", c.count, what, detail, endpoint)
	c.count = 0
	c.last = now
}

// describeDrop returns a stable reason, which keys the rate limit, and the text
// for the line. The URL in a client error is left out: the line prints the
// endpoint once.
func describeDrop(err error) (reason, detail string) {
	var rejected *responseError
	if errors.As(err, &rejected) {
		return rejected.reason(), rejected.Error()
	}
	var network *url.Error
	if errors.As(err, &network) {
		return "unreachable", "unreachable: " + network.Err.Error()
	}
	return "failed", "failed: " + err.Error()
}
