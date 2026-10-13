package bugbarn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Check-in statuses accepted by CheckIn.
const (
	CheckInOK    = "ok"
	CheckInError = "error"
)

// CheckInOption configures a single CheckIn call.
type CheckInOption func(*checkInOpts)

type checkInOpts struct {
	interval time.Duration
	grace    time.Duration
	message  string
}

// CheckInInterval declares how often the job is expected to check in
// (1 minute to 31 days). Sent on the first check-in it creates the monitor;
// sent later it replaces the stored value.
func CheckInInterval(d time.Duration) CheckInOption {
	return func(o *checkInOpts) { o.interval = d }
}

// CheckInGrace declares how long after the interval a check-in may still
// arrive before the monitor counts as missed.
func CheckInGrace(d time.Duration) CheckInOption {
	return func(o *checkInOpts) { o.grace = d }
}

// CheckInMessage attaches a short note (cut at 200 characters by the server).
// It only appears on the issue an error check-in opens, so keep personal data
// out of it.
func CheckInMessage(msg string) CheckInOption {
	return func(o *checkInOpts) { o.message = msg }
}

type checkInBody struct {
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
	Interval string `json:"interval,omitempty"`
	Grace    string `json:"grace,omitempty"`
}

// CheckIn tells BugBarn that a scheduled job or worker ran. Status is
// CheckInOK or CheckInError. A monitor that stays silent past its interval
// plus grace opens an issue; an ok check-in resolves it again.
//
// Declare the schedule with CheckInInterval on the call that runs every time:
//
//	bb.CheckIn("pending-sweep", bb.CheckInOK, bb.CheckInInterval(time.Hour), bb.CheckInGrace(10*time.Minute))
//
// Delivery is asynchronous and never blocks the caller. It returns false when
// Init has not run or the status is unknown. Flush and Shutdown wait
// for check-ins in flight.
func CheckIn(slug, status string, options ...CheckInOption) bool {
	mu.Lock()
	t := tp
	mu.Unlock()
	if t == nil || slug == "" || (status != CheckInOK && status != CheckInError) {
		return false
	}
	var co checkInOpts
	for _, o := range options {
		o(&co)
	}
	body := checkInBody{Status: status, Message: co.message}
	if co.interval > 0 {
		body.Interval = co.interval.String()
	}
	if co.grace > 0 {
		body.Grace = co.grace.String()
	}
	t.inflight.Add(1)
	go func() {
		defer t.inflight.Done()
		_ = t.sendCheckIn(slug, body)
	}()
	return true
}

// checkInURL derives the monitors endpoint from the events endpoint.
func checkInURL(endpoint, slug string) string {
	base := endpoint
	if i := strings.Index(endpoint, "/api/"); i >= 0 {
		base = endpoint[:i]
	}
	return strings.TrimRight(base, "/") + "/api/v1/monitors/" + slug + "/check-ins"
}

func (t *transport) sendCheckIn(slug string, body checkInBody) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, checkInURL(t.endpoint, slug), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BugBarn-Api-Key", t.apiKey)
	if t.projectSlug != "" {
		req.Header.Set("X-BugBarn-Project", t.projectSlug)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
