package hostmetrics

import (
	"sort"
	"time"
)

// rateDef maps a Vector counter to the per-second metric derived from it.
type rateDef struct {
	prefix string
	suffix string
	skip   func(dev string) bool
}

var rateDefs = map[string]rateDef{
	"network_receive_bytes_total":  {prefix: "net", suffix: "rx_bytes_per_s", skip: skipNetDevice},
	"network_transmit_bytes_total": {prefix: "net", suffix: "tx_bytes_per_s", skip: skipNetDevice},
	"disk_read_bytes_total":        {prefix: "disk", suffix: "read_bytes_per_s", skip: skipDiskDevice},
	"disk_written_bytes_total":     {prefix: "disk", suffix: "write_bytes_per_s", skip: skipDiskDevice},
}

func (b *batch) applyRate(st *hostState, e *event, minute time.Time, def rateDef) {
	dev := e.tags["device"]
	if dev == "" || def.skip(dev) {
		return
	}
	key := e.name + "|" + dev
	prev, had := st.counters[key]
	if had && !e.ts.After(prev.ts) {
		return
	}
	st.counters[key] = counterPoint{value: e.value, ts: e.ts}
	if !had {
		return
	}
	delta := e.value - prev.value
	if delta < 0 {
		return
	}
	rate := delta / e.ts.Sub(prev.ts).Seconds()
	b.out.add(e.host, def.prefix+"."+dev+"."+def.suffix, minute, rate)
}

// addCPU aggregates one cpu_seconds_total line into its (host, timestamp)
// scrape; Vector emits one line per cpu and mode with a shared timestamp.
func (b *batch) addCPU(e *event) {
	scrapes, ok := b.cpu[e.host]
	if !ok {
		scrapes = map[int64]*cpuScrape{}
		b.cpu[e.host] = scrapes
	}
	k := e.ts.UnixNano()
	s, ok := scrapes[k]
	if !ok {
		s = &cpuScrape{ts: e.ts, cpus: map[string]struct{}{}}
		scrapes[k] = s
	}
	s.total += e.value
	if mode := e.tags["mode"]; mode == "idle" || mode == "iowait" {
		s.idle += e.value
	}
	if cpu := e.tags["cpu"]; cpu != "" {
		s.cpus[cpu] = struct{}{}
	}
}

func (n *Normalizer) applyCPU(b *batch) {
	for host, scrapes := range b.cpu {
		st := n.hosts[host]
		for _, s := range sortScrapes(scrapes) {
			if len(s.cpus) > 0 {
				st.cores = len(s.cpus)
			}
			if util, ok := st.cpu.next(s); ok {
				b.out.add(host, "cpu.util", s.ts.Truncate(time.Minute), util)
			}
		}
	}
}

func sortScrapes(m map[int64]*cpuScrape) []*cpuScrape {
	out := make([]*cpuScrape, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ts.Before(out[j].ts) })
	return out
}
