// Package detect turns telemetry into detections. Security rules count
// matching log records per group key in a sliding window; metric rules fire
// when a host series stays past a threshold for a duration; the heartbeat
// fires when a host stops reporting. Each detection becomes a BugBarn event
// with a stable fingerprint, so it lands in one issue per rule and group and
// gets triage, resolve/regress and alerting from the existing pipeline.
package detect

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Tracks a rule can belong to.
const (
	TrackSecurity  = "security"
	TrackMetrics   = "metrics"
	TrackHeartbeat = "heartbeat"
)

// Rule is one detection rule. The JSON shape is shared by defaults.json, the
// detection_rules table and the rules API.
type Rule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Severity string `json:"severity"` // WARNING, ERROR or CRITICAL
	Track    string `json:"track"`

	// Security: records matching every condition are counted per GroupBy
	// key; Threshold within Window fires.
	Match   []Cond   `json:"match,omitempty"`
	GroupBy []string `json:"group_by,omitempty"`

	// Metrics: series whose name matches Metric (a glob where * matches any
	// run of characters, slashes included, e.g. "fs.*.used_pct") compared with Op against Threshold, multiplied by the
	// host's core count when PerCore is set. Fires after holding for For.
	Metric  string   `json:"metric,omitempty"`
	Op      string   `json:"op,omitempty"` // gt or lt
	PerCore bool     `json:"per_core,omitempty"`
	For     Duration `json:"for,omitempty"`

	// Threshold is a record count for security rules and a value for metric
	// rules. Heartbeat rules use Window as the allowed silence.
	Threshold float64  `json:"threshold"`
	Window    Duration `json:"window,omitempty"`
	// Cooldown is the minimum time between two detections for one group.
	Cooldown Duration `json:"cooldown,omitempty"`
}

// Cond matches one secnorm.Record field.
type Cond struct {
	Field string `json:"field"`
	Op    string `json:"op"` // eq, ne, in, not_in, prefix, not_prefix, contains
	Value any    `json:"value"`
}

// Duration is a time.Duration that reads and writes as "5m".
type Duration time.Duration

// D returns d as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// MarshalJSON writes the duration as a Go duration string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts a Go duration string or a number of seconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(v)
		return nil
	}
	var secs float64
	if err := json.Unmarshal(b, &secs); err != nil {
		return fmt.Errorf("duration must be a string like \"5m\" or seconds: %w", err)
	}
	*d = Duration(time.Duration(secs * float64(time.Second)))
	return nil
}

var validSeverity = map[string]bool{"WARNING": true, "ERROR": true, "CRITICAL": true}

// Validate reports the first problem that would make the rule misbehave.
func (r Rule) Validate() error {
	if r.ID == "" || strings.ContainsAny(r.ID, " :/") {
		return errors.New("id is required and may not contain spaces, ':' or '/'")
	}
	if r.Name == "" {
		return errors.New("name is required")
	}
	if !validSeverity[r.Severity] {
		return errors.New("severity must be WARNING, ERROR or CRITICAL")
	}
	switch r.Track {
	case TrackSecurity:
		return r.validateSecurity()
	case TrackMetrics:
		return r.validateMetrics()
	case TrackHeartbeat:
		if r.Window <= 0 {
			return errors.New("heartbeat rules need a window")
		}
		return nil
	}
	return errors.New("track must be security, metrics or heartbeat")
}

func (r Rule) validateSecurity() error {
	if len(r.Match) == 0 {
		return errors.New("security rules need at least one match condition")
	}
	for _, c := range r.Match {
		if err := c.validate(); err != nil {
			return err
		}
	}
	for _, f := range r.GroupBy {
		if !knownField(f) {
			return fmt.Errorf("unknown group_by field %q", f)
		}
	}
	if r.Threshold < 1 {
		return errors.New("security rules need a threshold of at least 1")
	}
	if r.Threshold > 1 && r.Window <= 0 {
		return errors.New("a threshold above 1 needs a window")
	}
	return nil
}

func (r Rule) validateMetrics() error {
	if r.Metric == "" {
		return errors.New("metric rules need a metric")
	}
	if r.Op != "gt" && r.Op != "lt" {
		return errors.New("op must be gt or lt")
	}
	return nil
}

var knownFields = map[string]bool{
	"source": true, "host": true, "kind": true, "src_ip": true, "user": true,
	"action": true, "status": true, "method": true, "path": true, "message": true,
}

func knownField(f string) bool { return knownFields[f] }

func (c Cond) validate() error {
	if !knownField(c.Field) {
		return fmt.Errorf("unknown match field %q", c.Field)
	}
	switch c.Op {
	case "eq", "ne", "prefix", "not_prefix", "contains":
		if _, ok := c.Value.(string); !ok {
			if _, isNum := c.Value.(float64); !isNum {
				return fmt.Errorf("%s on %s needs a single value", c.Op, c.Field)
			}
		}
	case "in", "not_in":
		if _, ok := c.Value.([]any); !ok {
			return fmt.Errorf("%s on %s needs a list", c.Op, c.Field)
		}
	default:
		return fmt.Errorf("unknown op %q", c.Op)
	}
	return nil
}

// matches reports whether v satisfies the condition.
func (c Cond) matches(v string) bool {
	switch c.Op {
	case "eq":
		return v == scalar(c.Value)
	case "ne":
		return v != scalar(c.Value)
	case "prefix":
		return strings.HasPrefix(v, scalar(c.Value))
	case "not_prefix":
		return !strings.HasPrefix(v, scalar(c.Value))
	case "contains":
		return strings.Contains(v, scalar(c.Value))
	case "in":
		return inList(v, c.Value)
	case "not_in":
		return !inList(v, c.Value)
	}
	return false
}

func inList(v string, list any) bool {
	items, _ := list.([]any)
	for _, it := range items {
		if v == scalar(it) {
			return true
		}
	}
	return false
}

// scalar renders a JSON value the way Record.Field renders fields, so a rule
// can say `"value": 401` or `"value": "401"`.
func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// globMatch reports whether name matches pattern, where * matches any run of
// characters (mount points contain slashes, so path.Match does not fit).
func globMatch(pattern, name string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == name
	}
	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	name = name[len(parts[0]):]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(name, mid)
		if i < 0 {
			return false
		}
		name = name[i+len(mid):]
	}
	return strings.HasSuffix(name, parts[len(parts)-1])
}
