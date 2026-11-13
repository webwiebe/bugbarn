package selflog

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	logShipTimeout = 5 * time.Second
	// logShipSlots bounds the requests in flight. A full shipper drops the line:
	// shipping is a measurement and never slows or blocks the caller.
	logShipSlots = 4
)

// LogShipper sends single log lines to the log ingest of the BugBarn named by
// BUGBARN_SELF_ENDPOINT, with the self-reporting key, so a line below ERROR
// that selflog does not turn into an issue still reaches `bb logs`. It needs
// no key of its own and no database, so readers use it as well as the writer.
// A failed send drops the line; it logs nothing, because an ERROR record would
// file an issue and a WARN would repeat what the sender already knows.
type LogShipper struct {
	url     string
	apiKey  string
	project string
	client  *http.Client
	slots   chan struct{}
	now     func() time.Time
	// done is called after each send attempt; tests use it to wait.
	done func(err error)
}

// NewLogShipper returns a shipper for the logs endpoint derived from endpoint,
// which takes the same forms as the SDK's: a base URL or a full /api/ URL.
func NewLogShipper(endpoint, apiKey, project string) *LogShipper {
	return &LogShipper{
		url:     LogsURL(endpoint),
		apiKey:  apiKey,
		project: project,
		client:  &http.Client{Timeout: logShipTimeout},
		slots:   make(chan struct{}, logShipSlots),
		now:     time.Now,
	}
}

// LogsURL maps the self endpoint to the log ingest URL.
func LogsURL(endpoint string) string {
	if i := strings.Index(endpoint, "/api/"); i >= 0 {
		endpoint = endpoint[:i]
	}
	return strings.TrimRight(endpoint, "/") + "/api/v1/logs"
}

// Ship sends one line at the given level with attrs as its fields, without
// blocking. It drops the line when logShipSlots sends are already in flight.
func (s *LogShipper) Ship(level, msg string, attrs map[string]any) {
	select {
	case s.slots <- struct{}{}:
	default:
		return
	}
	line := make(map[string]any, len(attrs)+3)
	for k, v := range attrs {
		line[k] = v
	}
	line["time"] = s.now().UnixMilli()
	line["level"] = level
	line["msg"] = msg
	go func() {
		defer func() { <-s.slots }()
		err := s.send(line)
		if s.done != nil {
			s.done(err)
		}
	}()
}

func (s *LogShipper) send(line map[string]any) error {
	body, err := json.Marshal(map[string]any{"logs": []map[string]any{line}})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), logShipTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BugBarn-Api-Key", s.apiKey)
	if s.project != "" {
		req.Header.Set("X-BugBarn-Project", s.project)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return &statusError{code: resp.StatusCode}
	}
	return nil
}

type statusError struct{ code int }

func (e *statusError) Error() string { return "log ingest answered " + http.StatusText(e.code) }
