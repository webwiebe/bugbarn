// Package secnorm turns security log lines shipped by Vector (Traefik access
// logs, Kubernetes audit events, journald sshd/sudo, macOS unified log) and
// Cloudflare firewall events into one normalized Record shape.
//
// Vector only tags each line with `source` and `host`; all parsing happens
// here, in Go, where every format has fixture tests. Detection rules
// (internal/detect) match on Record fields, so a field this package fails to
// fill is a field no rule can see.
package secnorm

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// Record is one normalized security log line.
type Record struct {
	TS      time.Time
	Source  string // traefik, k8saudit, sshd, sudo, journald, macos, cloudflare, or the shipped hint
	Host    string // reporting node
	Kind    string // per source: http, auth_failure, invalid_user, auth_success, sudo, <k8s resource>, <cloudflare source>
	SrcIP   string
	User    string
	Action  string // http method class, ssh auth method, k8s verb, cloudflare action
	Status  int
	Method  string
	Path    string
	Message string
	Raw     string
}

// Field returns the named field as a string, for rule matching.
func (r Record) Field(name string) (string, bool) {
	switch name {
	case "source":
		return r.Source, true
	case "host":
		return r.Host, true
	case "kind":
		return r.Kind, true
	case "src_ip":
		return r.SrcIP, true
	case "user":
		return r.User, true
	case "action":
		return r.Action, true
	case "status":
		return r.StatusString(), true
	case "method":
		return r.Method, true
	case "path":
		return r.Path, true
	case "message":
		return r.Message, true
	}
	return "", false
}

// MaxRawBytes bounds the raw line kept per record.
const MaxRawBytes = 8 << 10

// telemetryPathPrefix is BugBarn's own telemetry ingest. Vector's posts show up
// in the Traefik access log it ships; dropping them here (Vector drops them
// too) keeps the pipeline from feeding on itself.
const telemetryPathPrefix = "/api/v1/telemetry/"

// Parse decodes an NDJSON body (or a JSON array) of Vector events and returns
// the normalized records plus how many lines could not be decoded.
func Parse(body []byte, now time.Time) (records []Record, skipped int) {
	for _, obj := range splitBody(body, &skipped) {
		rec, ok := normalize(obj, now)
		if !ok {
			continue
		}
		records = append(records, rec)
	}
	return records, skipped
}

func splitBody(body []byte, skipped *int) []map[string]any {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []map[string]any
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			*skipped++
			return nil
		}
		return arr
	}
	var out []map[string]any
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			*skipped++
			continue
		}
		out = append(out, obj)
	}
	return out
}

// normalize dispatches one Vector event to its source parser. The source hint
// wins; without one the parser is picked from the payload's shape.
func normalize(ev map[string]any, now time.Time) (Record, bool) {
	inner := innerJSON(ev)
	base := Record{
		Source: str(ev, "source"),
		Host:   firstNonEmpty(str(ev, "host"), str(ev, "hostname")),
		TS:     eventTime(ev, now),
		Raw:    truncate(rawOf(ev, inner), MaxRawBytes),
	}
	var rec Record
	var ok bool
	switch detectSource(base.Source, ev, inner) {
	case "traefik":
		rec, ok = traefik(base, inner)
	case "k8saudit":
		rec, ok = k8sAudit(base, inner)
	case "macos":
		rec, ok = macOSLog(base, inner)
	case "cloudflare":
		rec, ok = cloudflare(base, firstMap(inner, ev))
	case "syslog":
		rec, ok = syslogLine(base, ev)
	default:
		rec, ok = generic(base, ev), true
	}
	if ok && rec.Source == "traefik" && strings.HasPrefix(rec.Path, telemetryPathPrefix) {
		return Record{}, false
	}
	return rec, ok
}

func detectSource(hint string, ev, inner map[string]any) string {
	switch hint {
	case "traefik", "k8saudit", "macos", "cloudflare":
		return hint
	case "journald", "sshd", "sudo", "syslog", "auth":
		return "syslog"
	}
	switch {
	case inner != nil && has(inner, "DownstreamStatus", "RequestMethod"):
		return "traefik"
	case inner != nil && has(inner, "auditID"):
		return "k8saudit"
	case inner != nil && has(inner, "eventMessage"):
		return "macos"
	case has(ev, "SYSLOG_IDENTIFIER", "_COMM", "appname"):
		return "syslog"
	}
	return ""
}

func generic(base Record, ev map[string]any) Record {
	base.Kind = "other"
	base.Message = truncate(str(ev, "message"), 1024)
	return base
}

// innerJSON returns the event's `message` decoded as a JSON object when it is
// one (Traefik, audit and `log stream` lines all arrive that way), or the
// event itself when the line was already structured.
func innerJSON(ev map[string]any) map[string]any {
	msg, ok := ev["message"].(string)
	if !ok {
		if m, isMap := ev["message"].(map[string]any); isMap {
			return m
		}
		return nil
	}
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "{") {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal([]byte(msg), &obj) != nil {
		return nil
	}
	return obj
}

func rawOf(ev, inner map[string]any) string {
	if msg, ok := ev["message"].(string); ok && inner != nil {
		return msg
	}
	b, _ := json.Marshal(ev)
	return string(b)
}

func eventTime(ev map[string]any, now time.Time) time.Time {
	for _, k := range []string{"timestamp", "time", "ts"} {
		if s := str(ev, k); s != "" {
			if t, ok := parseTime(s); ok {
				return t
			}
		}
	}
	return now.UTC()
}

func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.000000-0700", "2006-01-02 15:04:05-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
