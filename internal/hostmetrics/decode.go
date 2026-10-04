package hostmetrics

import (
	"bytes"
	"encoding/json"
	"time"
)

// rawEvent is the subset of a Vector metric event we read.
type rawEvent struct {
	Name      string            `json:"name"`
	Host      string            `json:"host"`
	Tags      map[string]string `json:"tags"`
	Timestamp string            `json:"timestamp"`
	Gauge     *rawValue         `json:"gauge"`
	Counter   *rawValue         `json:"counter"`
}

type rawValue struct {
	Value float64 `json:"value"`
}

// event is a decoded, validated metric event.
type event struct {
	host  string
	name  string
	tags  map[string]string
	ts    time.Time
	value float64
}

// decodeBody accepts NDJSON or a JSON array and returns the valid events plus
// the number of entries that could not be used.
func decodeBody(body []byte, now time.Time) ([]event, int) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return decodeArray(trimmed, now)
	}
	return decodeLines(trimmed, now)
}

func decodeArray(body []byte, now time.Time) ([]event, int) {
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, 1
	}
	return decodeAll(items, now)
}

func decodeLines(body []byte, now time.Time) ([]event, int) {
	var items []json.RawMessage
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			items = append(items, line)
		}
	}
	return decodeAll(items, now)
}

func decodeAll(items []json.RawMessage, now time.Time) ([]event, int) {
	events := make([]event, 0, len(items))
	skipped := 0
	for _, raw := range items {
		e, ok := decodeOne(raw, now)
		if !ok {
			skipped++
			continue
		}
		events = append(events, e)
	}
	return events, skipped
}

func decodeOne(raw []byte, now time.Time) (event, bool) {
	var r rawEvent
	if err := json.Unmarshal(raw, &r); err != nil {
		return event{}, false
	}
	host := r.Tags["host"]
	if host == "" {
		host = r.Host
	}
	if r.Name == "" || host == "" {
		return event{}, false
	}
	v := r.Gauge
	if v == nil {
		v = r.Counter
	}
	if v == nil {
		return event{}, false
	}
	return event{host: host, name: r.Name, tags: r.Tags, ts: parseTS(r.Timestamp, now), value: v.Value}, true
}

func parseTS(s string, now time.Time) time.Time {
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return now.UTC()
	}
	return ts.UTC()
}
