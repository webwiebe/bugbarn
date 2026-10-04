// Package hostmetrics turns Vector host_metrics events into per-minute samples.
package hostmetrics

import (
	"sort"
	"sync"
	"time"
)

// stateTTL bounds memory: hosts not seen for this long lose their counter state.
const stateTTL = time.Hour

// Sample is one per-minute value for one host series.
type Sample struct {
	Host   string
	Metric string    // e.g. "load1", "cpu.util", "mem.avail_pct", "fs./.used_pct", "net.eth0.rx_bytes_per_s", "disk.vda.read_bytes_per_s"
	TS     time.Time // UTC, truncated to the minute
	Value  float64
}

// HostInfo is what the normalizer learned about a host in this batch.
type HostInfo struct {
	Host     string
	Cores    int       // distinct cpu tags seen for cpu_seconds_total; 0 if unknown
	LastSeen time.Time // newest event timestamp for the host
}

// Normalizer is stateful (counter deltas) and safe for concurrent use.
type Normalizer struct {
	mu    sync.Mutex
	now   func() time.Time
	hosts map[string]*hostState
}

// NewNormalizer returns an empty Normalizer.
func NewNormalizer() *Normalizer {
	return &Normalizer{now: time.Now, hosts: map[string]*hostState{}}
}

// batch is the working set of one Parse call.
type batch struct {
	out  sampleSet
	info map[string]*HostInfo
	cpu  map[string]map[int64]*cpuScrape
}

// Parse decodes an NDJSON (or JSON array) body and returns derived samples,
// per-host info and the count of skipped lines.
func (n *Normalizer) Parse(body []byte) (samples []Sample, hosts []HostInfo, skipped int) {
	n.mu.Lock()
	defer n.mu.Unlock()

	now := n.now()
	events, skipped := decodeBody(body, now)
	n.evict(now)

	b := &batch{info: map[string]*HostInfo{}, cpu: map[string]map[int64]*cpuScrape{}}
	for i := range events {
		n.apply(b, &events[i], now)
	}
	n.applyCPU(b)
	return b.sortedSamples(), n.hostInfos(b), skipped
}

func (n *Normalizer) evict(now time.Time) {
	for host, st := range n.hosts {
		if now.Sub(st.seenAt) > stateTTL {
			delete(n.hosts, host)
		}
	}
}

func (n *Normalizer) state(host string, now time.Time) *hostState {
	st, ok := n.hosts[host]
	if !ok {
		st = newHostState()
		n.hosts[host] = st
	}
	st.seenAt = now
	return st
}

func (n *Normalizer) apply(b *batch, e *event, now time.Time) {
	st := n.state(e.host, now)
	b.touch(e)
	minute := e.ts.Truncate(time.Minute)
	switch e.name {
	case "load1", "load5", "load15":
		b.out.add(e.host, e.name, minute, e.value)
	case "memory_total_bytes", "memory_available_bytes", "memory_free_bytes":
		b.applyMemory(st, e, minute)
	case "filesystem_total_bytes", "filesystem_used_bytes":
		b.applyFS(st, e, minute)
	case "cpu_seconds_total":
		b.addCPU(e)
	default:
		if def, ok := rateDefs[e.name]; ok {
			b.applyRate(st, e, minute, def)
		}
	}
}

func (b *batch) touch(e *event) {
	hi, ok := b.info[e.host]
	if !ok {
		hi = &HostInfo{Host: e.host}
		b.info[e.host] = hi
	}
	if e.ts.After(hi.LastSeen) {
		hi.LastSeen = e.ts
	}
}

func (b *batch) applyMemory(st *hostState, e *event, minute time.Time) {
	var pct float64
	var ok bool
	switch e.name {
	case "memory_total_bytes":
		pct, ok = st.mem.setTotal(e.value, minute)
	case "memory_available_bytes":
		st.sawAvailable = true
		pct, ok = st.mem.setPart(e.value, minute)
	default: // memory_free_bytes, only for hosts that never report available
		if st.sawAvailable {
			return
		}
		pct, ok = st.mem.setPart(e.value, minute)
	}
	if !ok {
		return
	}
	b.out.add(e.host, "mem.avail_pct", minute, pct)
	b.out.add(e.host, "mem.used_pct", minute, 100-pct)
}

func (b *batch) applyFS(st *hostState, e *event, minute time.Time) {
	if skipFSTypes[e.tags["filesystem"]] {
		return
	}
	mp, keep := normalizeMount(e.tags["mountpoint"])
	if !keep {
		return
	}
	r, ok := st.fs[mp]
	if !ok {
		r = &ratio{}
		st.fs[mp] = r
	}
	var pct float64
	if e.name == "filesystem_total_bytes" {
		pct, ok = r.setTotal(e.value, minute)
	} else {
		pct, ok = r.setPart(e.value, minute)
	}
	if ok {
		b.out.add(e.host, "fs."+mp+".used_pct", minute, pct)
	}
}

func (b *batch) sortedSamples() []Sample {
	out := b.out.list
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		if out[i].Metric != out[j].Metric {
			return out[i].Metric < out[j].Metric
		}
		return out[i].TS.Before(out[j].TS)
	})
	return out
}

func (n *Normalizer) hostInfos(b *batch) []HostInfo {
	out := make([]HostInfo, 0, len(b.info))
	for host, hi := range b.info {
		if st, ok := n.hosts[host]; ok {
			hi.Cores = st.cores
		}
		out = append(out, *hi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}
