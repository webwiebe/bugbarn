package hostmetrics

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 4, 10, 0, 3, 123e6, time.UTC)

func line(name, kind string, ts time.Time, v float64, tags map[string]string) string {
	if tags == nil {
		tags = map[string]string{}
	}
	if _, ok := tags["host"]; !ok {
		tags["host"] = "k3s1"
	}
	ev := map[string]any{
		"name": name, "namespace": "host", "tags": tags,
		"timestamp": ts.Format(time.RFC3339Nano), "kind": "absolute",
		kind: map[string]float64{"value": v},
	}
	b, _ := json.Marshal(ev)
	return string(b)
}

func gauge(name string, ts time.Time, v float64, tags map[string]string) string {
	return line(name, "gauge", ts, v, tags)
}

func counter(name string, ts time.Time, v float64, tags map[string]string) string {
	return line(name, "counter", ts, v, tags)
}

func body(lines ...string) []byte { return []byte(strings.Join(lines, "\n")) }

func newTestNormalizer(clock *time.Time) *Normalizer {
	n := NewNormalizer()
	n.now = func() time.Time { return *clock }
	return n
}

func find(samples []Sample, metric string) (Sample, bool) {
	for _, s := range samples {
		if s.Metric == metric {
			return s, true
		}
	}
	return Sample{}, false
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func wantValue(t *testing.T, samples []Sample, metric string, want float64) {
	t.Helper()
	s, ok := find(samples, metric)
	if !ok {
		t.Fatalf("missing %s in %+v", metric, samples)
	}
	if !approx(s.Value, want) {
		t.Fatalf("%s = %v, want %v", metric, s.Value, want)
	}
}

func wantAbsent(t *testing.T, samples []Sample, metric string) {
	t.Helper()
	if s, ok := find(samples, metric); ok {
		t.Fatalf("unexpected %s = %v", metric, s.Value)
	}
}

func TestGauges(t *testing.T) {
	clock := t0
	n := newTestNormalizer(&clock)
	samples, hosts, skipped := n.Parse(body(
		gauge("load1", t0, 0.52, nil),
		gauge("load5", t0, 0.4, nil),
		gauge("load15", t0, 0.3, nil),
		gauge("some_unknown_metric", t0, 9, nil),
	))
	if skipped != 0 || len(samples) != 3 {
		t.Fatalf("skipped=%d samples=%+v", skipped, samples)
	}
	wantValue(t, samples, "load1", 0.52)
	wantValue(t, samples, "load5", 0.4)
	wantValue(t, samples, "load15", 0.3)
	wantMinute := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	for _, s := range samples {
		if !s.TS.Equal(wantMinute) || s.TS.Location() != time.UTC || s.Host != "k3s1" {
			t.Fatalf("bad sample %+v", s)
		}
	}
	if len(hosts) != 1 || !hosts[0].LastSeen.Equal(t0) || hosts[0].Cores != 0 {
		t.Fatalf("hosts = %+v", hosts)
	}
}

func TestMemory(t *testing.T) {
	later := t0.Add(5 * time.Second)
	tests := []struct {
		name      string
		batches   [][]string
		wantAvail float64 // NaN means absent in the final batch
	}{
		{"total before available", [][]string{{
			gauge("memory_total_bytes", t0, 8e9, nil), gauge("memory_available_bytes", later, 2e9, nil),
		}}, 25},
		{"available before total same minute", [][]string{{
			gauge("memory_available_bytes", t0, 2e9, nil), gauge("memory_total_bytes", later, 8e9, nil),
		}}, 25},
		{"available alone without total", [][]string{{gauge("memory_available_bytes", t0, 2e9, nil)}}, math.NaN()},
		{"total from earlier parse", [][]string{
			{gauge("memory_total_bytes", t0, 8e9, nil)},
			{gauge("memory_available_bytes", t0.Add(time.Minute), 4e9, nil)},
		}, 50},
		{"available from older minute ignored on total", [][]string{
			{gauge("memory_available_bytes", t0, 2e9, nil)},
			{gauge("memory_total_bytes", t0.Add(time.Minute), 8e9, nil)},
		}, math.NaN()},
		{"free fallback", [][]string{{
			gauge("memory_total_bytes", t0, 8e9, nil), gauge("memory_free_bytes", t0, 1e9, nil),
		}}, 12.5},
		{"available wins over free in same batch", [][]string{{
			gauge("memory_total_bytes", t0, 8e9, nil), gauge("memory_free_bytes", t0, 1e9, nil),
			gauge("memory_available_bytes", t0, 2e9, nil),
		}}, 25},
		{"free ignored once available was seen", [][]string{
			{gauge("memory_total_bytes", t0, 8e9, nil), gauge("memory_available_bytes", t0, 2e9, nil)},
			{gauge("memory_free_bytes", t0.Add(time.Minute), 1e9, nil)},
		}, math.NaN()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := t0
			n := newTestNormalizer(&clock)
			var samples []Sample
			for _, b := range tc.batches {
				samples, _, _ = n.Parse(body(b...))
			}
			if math.IsNaN(tc.wantAvail) {
				wantAbsent(t, samples, "mem.avail_pct")
				wantAbsent(t, samples, "mem.used_pct")
				return
			}
			wantValue(t, samples, "mem.avail_pct", tc.wantAvail)
			wantValue(t, samples, "mem.used_pct", 100-tc.wantAvail)
			if c := countMetric(samples, "mem.avail_pct"); c != 1 {
				t.Fatalf("mem.avail_pct emitted %d times", c)
			}
		})
	}
}

func countMetric(samples []Sample, metric string) int {
	c := 0
	for _, s := range samples {
		if s.Metric == metric {
			c++
		}
	}
	return c
}

func fsTags(mount, fstype string) map[string]string {
	return map[string]string{"collector": "filesystem", "device": "/dev/vda1", "filesystem": fstype, "mountpoint": mount}
}

func TestFilesystem(t *testing.T) {
	tests := []struct {
		mount, fstype string
		total, used   float64
		wantMetric    string // empty means nothing emitted
	}{
		{"/", "ext4", 100, 40, "fs./.used_pct"},
		{"/data", "xfs", 100, 40, "fs./data.used_pct"},
		{"/host", "ext4", 100, 40, "fs./.used_pct"},
		{"/host/var", "ext4", 100, 40, "fs./var.used_pct"},
		{"/hostdata", "ext4", 100, 40, "fs./hostdata.used_pct"},
		{"/System/Volumes/Data", "apfs", 100, 40, "fs./System/Volumes/Data.used_pct"},
		{"/devices", "ext4", 100, 40, "fs./devices.used_pct"},
		{"/", "ext4", 0, 0, ""},
		{"/tmp", "tmpfs", 100, 40, ""},
		{"/merged", "overlay", 100, 40, ""},
		{"/run/k3s", "ext4", 100, 40, ""},
		{"/host/run", "ext4", 100, 40, ""},
		{"/host/proc/1", "ext4", 100, 40, ""},
		{"/dev", "ext4", 100, 40, ""},
		{"/snap/core/1", "ext4", 100, 40, ""},
		{"/var/lib/kubelet/pods/x", "ext4", 100, 40, ""},
		{"/var/lib/docker/overlay2", "ext4", 100, 40, ""},
		{"/var/lib/rancher/k3s/agent/containerd/x", "ext4", 100, 40, ""},
		{"/System/Volumes/VM", "apfs", 100, 40, ""},
		{"/private/var/vm", "apfs", 100, 40, ""},
		{"/net", "autofs", 100, 40, ""},
	}
	for _, tc := range tests {
		t.Run(tc.mount+"_"+tc.fstype, func(t *testing.T) {
			clock := t0
			n := newTestNormalizer(&clock)
			samples, _, _ := n.Parse(body(
				gauge("filesystem_total_bytes", t0, tc.total, fsTags(tc.mount, tc.fstype)),
				gauge("filesystem_used_bytes", t0, tc.used, fsTags(tc.mount, tc.fstype)),
			))
			if tc.wantMetric == "" {
				if len(samples) != 0 {
					t.Fatalf("want nothing, got %+v", samples)
				}
				return
			}
			if len(samples) != 1 {
				t.Fatalf("samples = %+v", samples)
			}
			wantValue(t, samples, tc.wantMetric, 40)
		})
	}
}

func cpuLines(ts time.Time, idle, user, iowait float64, cpus int) []string {
	var out []string
	for i := 0; i < cpus; i++ {
		cpu := string(rune('0' + i))
		for mode, v := range map[string]float64{"idle": idle, "user": user, "iowait": iowait} {
			out = append(out, counter("cpu_seconds_total", ts, v, map[string]string{"collector": "cpu", "cpu": cpu, "mode": mode}))
		}
	}
	return out
}

func TestCPUUtil(t *testing.T) {
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)
	clock := t0
	n := newTestNormalizer(&clock)

	samples, hosts, _ := n.Parse(body(cpuLines(t0, 100, 50, 10, 2)...))
	wantAbsent(t, samples, "cpu.util")
	if hosts[0].Cores != 2 {
		t.Fatalf("cores = %d", hosts[0].Cores)
	}

	// Per cpu: idle +30, iowait +10, user +60 → dIdle 40 of dTotal 100 → 60%.
	clock = t1
	samples, _, _ = n.Parse(body(cpuLines(t1, 130, 110, 20, 2)...))
	wantValue(t, samples, "cpu.util", 60)
	if s, _ := find(samples, "cpu.util"); !s.TS.Equal(t1.Truncate(time.Minute)) {
		t.Fatalf("ts = %v", s.TS)
	}

	// Counter reset: totals drop, so no sample, but the next scrape works again.
	clock = t2
	samples, _, _ = n.Parse(body(cpuLines(t2, 1, 1, 0, 2)...))
	wantAbsent(t, samples, "cpu.util")
	t3 := t0.Add(3 * time.Minute)
	samples, _, _ = n.Parse(body(cpuLines(t3, 11, 11, 0, 2)...))
	wantValue(t, samples, "cpu.util", 50)
}

func TestCPUTwoScrapesInOneBody(t *testing.T) {
	clock := t0
	n := newTestNormalizer(&clock)
	t1 := t0.Add(15 * time.Second)
	lines := append(cpuLines(t1, 120, 70, 10, 4), cpuLines(t0, 100, 50, 10, 4)...)
	samples, hosts, _ := n.Parse(body(lines...))
	wantValue(t, samples, "cpu.util", 50)
	if hosts[0].Cores != 4 {
		t.Fatalf("cores = %d", hosts[0].Cores)
	}
}

func TestJSONArrayBody(t *testing.T) {
	clock := t0
	n := newTestNormalizer(&clock)
	arr := "[" + gauge("load1", t0, 1.5, nil) + ",\n" + gauge("load5", t0, 1.0, map[string]string{"host": "mac"}) + `, {"name":"load1"}]`
	samples, hosts, skipped := n.Parse([]byte(arr))
	if skipped != 1 || len(samples) != 2 || len(hosts) != 2 {
		t.Fatalf("skipped=%d samples=%+v hosts=%+v", skipped, samples, hosts)
	}
	if hosts[0].Host != "k3s1" || hosts[1].Host != "mac" {
		t.Fatalf("hosts = %+v", hosts)
	}
}

func TestMalformedInput(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantSkipped int
		wantSamples int
	}{
		{"garbage lines", "not json\n" + gauge("load1", t0, 1, nil) + "\n{broken\n\n", 2, 1},
		{"missing host", `{"name":"load1","timestamp":"2026-10-04T10:00:00Z","gauge":{"value":1}}`, 1, 0},
		{"missing value", `{"name":"load1","tags":{"host":"a"},"timestamp":"2026-10-04T10:00:00Z"}`, 1, 0},
		{"missing name", `{"tags":{"host":"a"},"gauge":{"value":1}}`, 1, 0},
		{"bad array", `[{"name":`, 1, 0},
		{"empty body", "", 0, 0},
		{"top-level host", `{"name":"load1","host":"top","timestamp":"2026-10-04T10:00:00Z","gauge":{"value":2}}`, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := t0
			n := newTestNormalizer(&clock)
			samples, _, skipped := n.Parse([]byte(tc.body))
			if skipped != tc.wantSkipped || len(samples) != tc.wantSamples {
				t.Fatalf("skipped=%d samples=%+v", skipped, samples)
			}
		})
	}
}

func TestMissingTimestampUsesClock(t *testing.T) {
	clock := time.Date(2026, 10, 4, 12, 34, 56, 0, time.UTC)
	n := newTestNormalizer(&clock)
	samples, hosts, _ := n.Parse([]byte(`{"name":"load1","tags":{"host":"a"},"timestamp":"nope","gauge":{"value":1}}`))
	if len(samples) != 1 || !samples[0].TS.Equal(clock.Truncate(time.Minute)) || !hosts[0].LastSeen.Equal(clock) {
		t.Fatalf("samples=%+v hosts=%+v", samples, hosts)
	}
}

func TestDedupeKeepsLastValuePerMinute(t *testing.T) {
	clock := t0
	n := newTestNormalizer(&clock)
	samples, _, _ := n.Parse(body(
		gauge("load1", t0, 1, nil),
		gauge("load1", t0.Add(30*time.Second), 2, nil),
		gauge("load1", t0.Add(time.Minute), 3, nil),
	))
	if len(samples) != 2 || samples[0].Value != 2 || samples[1].Value != 3 {
		t.Fatalf("samples = %+v", samples)
	}
}

func TestCoresPersistAcrossBatches(t *testing.T) {
	clock := t0
	n := newTestNormalizer(&clock)
	n.Parse(body(cpuLines(t0, 1, 1, 1, 3)...))
	_, hosts, _ := n.Parse(body(gauge("load1", t0.Add(time.Minute), 1, nil)))
	if hosts[0].Cores != 3 {
		t.Fatalf("cores = %d", hosts[0].Cores)
	}
}

func TestHostStateEviction(t *testing.T) {
	tests := []struct {
		name     string
		gap      time.Duration
		wantRate bool
	}{
		{"within ttl keeps state", 30 * time.Minute, true},
		{"past ttl evicts state", 61 * time.Minute, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := t0
			n := newTestNormalizer(&clock)
			tags := netTags("eth0")
			n.Parse(body(counter("network_receive_bytes_total", t0, 0, tags)))
			clock = t0.Add(tc.gap)
			// A second host keeps the normalizer busy so eviction runs on Parse.
			n.Parse(body(gauge("load1", clock, 1, map[string]string{"host": "other"})))
			samples, _, _ := n.Parse(body(counter("network_receive_bytes_total", clock, 60, tags)))
			_, got := find(samples, "net.eth0.rx_bytes_per_s")
			if got != tc.wantRate {
				t.Fatalf("rate emitted=%v, want %v (samples %+v)", got, tc.wantRate, samples)
			}
			if _, ok := n.hosts["k3s1"]; !ok {
				t.Fatalf("host state should be recreated after parse")
			}
		})
	}
}
