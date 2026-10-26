package detect

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// Persist stores one event record. retryable reports whether a failed
// attempt may succeed later (a locked database), as opposed to a payload the
// pipeline will never accept.
type Persist func(ctx context.Context, rec spool.Record) (retryable bool, err error)

// Emitter turns detections into BugBarn events in the telemetry project. One
// goroutine drains the engine's queue, so persisting never blocks ingest.
type Emitter struct {
	in      <-chan Detection
	persist Persist
	project string
	env     string
	log     *slog.Logger
	backoff []time.Duration
}

// NewEmitter wires an emitter for detections from in into project.
func NewEmitter(in <-chan Detection, persist Persist, project string, log *slog.Logger) *Emitter {
	return &Emitter{
		in: in, persist: persist, project: project,
		log:     log.With("component", "detect"),
		backoff: []time.Duration{time.Second, 5 * time.Second, 30 * time.Second},
	}
}

// WithEnvironment tags every emitted event with the deployment environment of
// the BugBarn instance that raised it, so the dashboard's environment filter and
// facet cover infrastructure issues too. Empty leaves events untagged.
func (e *Emitter) WithEnvironment(env string) *Emitter {
	e.env = env
	return e
}

// Run emits detections until ctx ends.
func (e *Emitter) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-e.in:
			e.send(ctx, d)
		}
	}
}

func (e *Emitter) send(ctx context.Context, d Detection) {
	rec, err := RecordIn(d, e.project, e.env)
	if err != nil {
		e.log.Error("detection could not be encoded", "rule", d.Rule.ID, "error", err)
		return
	}
	for attempt := 0; ; attempt++ {
		retryable, err := e.persist(ctx, rec)
		if err == nil {
			e.log.Info("detection fired", "rule", d.Rule.ID, "group", d.GroupKey())
			return
		}
		if ctx.Err() != nil {
			return
		}
		if !retryable || attempt >= len(e.backoff) {
			e.log.Error("detection lost: persist failed", "rule", d.Rule.ID, "group", d.GroupKey(), "error", err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.backoff[attempt]):
		}
	}
}

// Fingerprint is the issue identity of a detection: one issue per rule and
// group (per rule and UTC day when the rule sets issue_by "day"), so repeats land on the same issue and a resolved one regresses.
func Fingerprint(d Detection) string {
	if d.Rule.IssueBy == IssueByDay {
		return "detect:" + d.Rule.ID + ":day:" + d.At.UTC().Format("2006-01-02")
	}
	sum := sha256.Sum256([]byte(d.GroupKey()))
	return "detect:" + d.Rule.ID + ":" + hex.EncodeToString(sum[:8])
}

// Record builds the internal spool record for a detection. Internal keeps the
// source IPs in the event; they are the point of a security detection.
func Record(d Detection, project string) (spool.Record, error) {
	return RecordIn(d, project, "")
}

// RecordIn is Record for an instance running in the named environment.
func RecordIn(d Detection, project, env string) (spool.Record, error) {
	body, err := json.Marshal(payload(d, env))
	if err != nil {
		return spool.Record{}, err
	}
	return spool.Record{
		IngestID:    "detect-" + randomID(),
		ReceivedAt:  time.Now().UTC(),
		ContentType: "application/json",
		BodyBase64:  base64.StdEncoding.EncodeToString(body),
		ProjectSlug: project,
		Internal:    true,
	}, nil
}

func payload(d Detection, env string) map[string]any {
	group := make(map[string]any, len(d.Group))
	for _, kv := range d.Group {
		group[kv.Key] = kv.Value
	}
	detection := map[string]any{
		"rule":      d.Rule.ID,
		"rule_name": d.Rule.Name,
		"track":     d.Rule.Track,
		"group":     group,
		"limit":     d.Limit,
	}
	switch d.Rule.Track {
	case TrackSecurity:
		detection["count"] = d.Count
		detection["window"] = d.Rule.Window.D().String()
	case TrackMetrics:
		detection["value"] = d.Value
		detection["for"] = d.Rule.For.D().String()
	case TrackHeartbeat:
		detection["silent_seconds"] = d.Value
	}
	attrs := map[string]any{"detection": detection, "sample": d.Sample}
	if h, ok := group["host"]; ok {
		attrs["host"] = h
	}
	if env != "" {
		// The dashboard filter reads attributes.environment, the facet reads
		// deployment.environment; BugBarn has no top-level environment field.
		attrs["environment"] = env
		attrs["deployment.environment"] = env
	}
	return map[string]any{
		"body":              title(d),
		"severityText":      d.Rule.Severity,
		"observedTimestamp": d.At.UTC().Format(time.RFC3339Nano),
		"fingerprint":       Fingerprint(d),
		"attributes":        attrs,
		"resource":          map[string]any{"service.name": "bugbarn-detect"},
	}
}

// title is the event message, which becomes the issue title. It names the
// rule and the group and leaves counts out, so it reads the same every time.
func title(d Detection) string {
	if len(d.Group) == 0 {
		return d.Rule.Name
	}
	if d.Rule.IssueBy == IssueByDay {
		return fmt.Sprintf("%s: %s", d.Rule.Name, d.At.UTC().Format("2006-01-02"))
	}
	return fmt.Sprintf("%s: %s", d.Rule.Name, d.GroupKey())
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
