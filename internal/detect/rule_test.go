package detect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"fs.*.used_pct", "fs./.used_pct", true},
		{"fs.*.used_pct", "fs./var/lib/rancher.used_pct", true},
		{"fs.*.used_pct", "fs./.free", false},
		{"load1", "load1", true},
		{"load1", "load15", false},
		{"net.*", "net.eth0.rx_bytes_per_s", true},
		{"*.used_pct", "mem.used_pct", true},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.name); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	base := rule(t, "ssh-bruteforce")
	cases := map[string]func(r *Rule){
		"id with colon":   func(r *Rule) { r.ID = "a:b" },
		"bad severity":    func(r *Rule) { r.Severity = "INFO" },
		"bad track":       func(r *Rule) { r.Track = "x" },
		"unknown field":   func(r *Rule) { r.Match = []Cond{{Field: "nope", Op: "eq", Value: "x"}} },
		"in without list": func(r *Rule) { r.Match = []Cond{{Field: "kind", Op: "in", Value: "x"}} },
		"bad op":          func(r *Rule) { r.Match = []Cond{{Field: "kind", Op: "regex", Value: "x"}} },
		"no window":       func(r *Rule) { r.Window = 0 },
		"bad group_by":    func(r *Rule) { r.GroupBy = []string{"zone"} },
	}
	for name, mutate := range cases {
		r := base
		r.Match = append([]Cond(nil), base.Match...)
		mutate(&r)
		if r.Validate() == nil {
			t.Errorf("%s: Validate accepted it", name)
		}
	}
	m := rule(t, "disk-full")
	m.Op = "ge"
	if m.Validate() == nil {
		t.Error("metric op ge accepted")
	}
	m = rule(t, "disk-full")
	m.Match = []Cond{{Field: "source", Op: "eq", Value: "x"}}
	if m.Validate() == nil {
		t.Error("metric rule matching on source accepted")
	}
	h := rule(t, "host-silent")
	h.Match = []Cond{{Field: "host", Op: "in", Value: "x"}}
	if h.Validate() == nil {
		t.Error("heartbeat host in without a list accepted")
	}
	h.Match = []Cond{{Field: "host", Op: "ne", Value: "laptop"}}
	if err := h.Validate(); err != nil {
		t.Errorf("heartbeat host ne rejected: %v", err)
	}
}

func TestDurationJSON(t *testing.T) {
	var r Rule
	if err := json.Unmarshal([]byte(`{"window":"5m","cooldown":90}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Window.D() != 5*time.Minute || r.Cooldown.D() != 90*time.Second {
		t.Fatalf("window %v cooldown %v", r.Window.D(), r.Cooldown.D())
	}
	b, _ := json.Marshal(r.Window)
	if string(b) != `"5m0s"` {
		t.Fatalf("marshal = %s", b)
	}
}

func TestMerge(t *testing.T) {
	builtins := []Rule{{ID: "a", Threshold: 1}, {ID: "b", Threshold: 1}}
	stored := []Rule{{ID: "custom", Threshold: 3}, {ID: "b", Threshold: 9}}
	got := Merge(builtins, stored)
	if len(got) != 3 || got[0].ID != "a" || got[1].Threshold != 9 || got[2].ID != "custom" {
		t.Fatalf("Merge = %+v", got)
	}
}

func TestRecordPayload(t *testing.T) {
	d := Detection{
		Rule:  rule(t, "ssh-bruteforce"),
		Group: []KV{{Key: "src_ip", Value: "203.0.113.9"}},
		At:    t0, Count: 31, Limit: 30, Sample: "Failed password for root from 203.0.113.9",
	}
	rec, err := Record(d, "infra")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Internal || rec.ProjectSlug != "infra" || !strings.HasPrefix(rec.IngestID, "detect-") {
		t.Fatalf("record = %+v", rec)
	}
	body, _ := base64.StdEncoding.DecodeString(rec.BodyBase64)
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if p["body"] != "SSH brute force: src_ip=203.0.113.9" || p["severityText"] != "WARNING" {
		t.Fatalf("payload = %v", p)
	}
	if fp, _ := p["fingerprint"].(string); fp != Fingerprint(d) || !strings.HasPrefix(fp, "detect:ssh-bruteforce:") {
		t.Fatalf("fingerprint = %v", p["fingerprint"])
	}
	other := d
	other.Group = []KV{{Key: "src_ip", Value: "203.0.113.10"}}
	if Fingerprint(other) == Fingerprint(d) {
		t.Fatal("two groups share a fingerprint")
	}
	det := p["attributes"].(map[string]any)["detection"].(map[string]any)
	if det["count"].(float64) != 31 || det["window"] != "5m0s" {
		t.Fatalf("detection attrs = %v", det)
	}
}

func TestEmitterRetriesTransientOnly(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func(retryable bool, failures int) int {
		in := make(chan Detection, 1)
		calls := 0
		e := NewEmitter(in, func(context.Context, spool.Record) (bool, error) {
			calls++
			if calls <= failures {
				return retryable, errors.New("boom")
			}
			return false, nil
		}, "infra", quiet)
		e.backoff = []time.Duration{0, 0, 0}
		e.send(context.Background(), Detection{Rule: rule(t, "host-silent"), Group: []KV{{Key: "host", Value: "h"}}, At: t0})
		return calls
	}
	if n := run(true, 2); n != 3 {
		t.Errorf("transient failure twice: %d calls, want 3", n)
	}
	if n := run(true, 10); n != 4 {
		t.Errorf("transient failure forever: %d calls, want 4 (1 + 3 retries)", n)
	}
	if n := run(false, 1); n != 1 {
		t.Errorf("permanent failure: %d calls, want 1", n)
	}
}

func TestSpoolRecordNeverSerializesInternal(t *testing.T) {
	b, _ := json.Marshal(spool.Record{IngestID: "x", Internal: true})
	if strings.Contains(strings.ToLower(string(b)), "internal") {
		t.Fatalf("Internal leaked into JSON: %s", b)
	}
	var r spool.Record
	_ = json.Unmarshal([]byte(`{"ingestId":"x","Internal":true,"internal":true}`), &r)
	if r.Internal {
		t.Fatal("Internal was read from JSON")
	}
}
