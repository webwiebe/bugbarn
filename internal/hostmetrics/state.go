package hostmetrics

import "time"

// hostState is what the normalizer remembers about a host between Parse calls.
type hostState struct {
	seenAt       time.Time
	cores        int
	mem          ratio
	sawAvailable bool
	fs           map[string]*ratio
	cpu          cpuState
	counters     map[string]counterPoint
}

func newHostState() *hostState {
	return &hostState{fs: map[string]*ratio{}, counters: map[string]counterPoint{}}
}

// ratio tracks part/total*100 where total may arrive in any minute but the
// part must belong to the minute being emitted.
type ratio struct {
	total   float64
	part    float64
	partMin int64
	hasPart bool
}

func (r *ratio) setTotal(v float64, minute time.Time) (float64, bool) {
	r.total = v
	if !r.hasPart || r.partMin != minute.Unix() || v <= 0 {
		return 0, false
	}
	return r.part / v * 100, true
}

func (r *ratio) setPart(v float64, minute time.Time) (float64, bool) {
	r.part, r.partMin, r.hasPart = v, minute.Unix(), true
	if r.total <= 0 {
		return 0, false
	}
	return v / r.total * 100, true
}

// cpuState holds the previous aggregated scrape of a host.
type cpuState struct {
	total float64
	idle  float64
	ts    time.Time
	has   bool
}

// next advances to scrape s and returns the utilization since the previous one.
func (c *cpuState) next(s *cpuScrape) (float64, bool) {
	if c.has && !s.ts.After(c.ts) {
		return 0, false
	}
	prev := *c
	*c = cpuState{total: s.total, idle: s.idle, ts: s.ts, has: true}
	if !prev.has {
		return 0, false
	}
	dTotal, dIdle := s.total-prev.total, s.idle-prev.idle
	if dTotal <= 0 || dIdle < 0 || dIdle > dTotal {
		return 0, false
	}
	return 100 * (1 - dIdle/dTotal), true
}

type counterPoint struct {
	value float64
	ts    time.Time
}

// cpuScrape is the sum of all cpu_seconds_total lines sharing one timestamp.
type cpuScrape struct {
	ts    time.Time
	total float64
	idle  float64
	cpus  map[string]struct{}
}

// sampleSet collects samples, keeping the last value per host+metric+minute.
type sampleSet struct {
	idx  map[sampleKey]int
	list []Sample
}

type sampleKey struct {
	host   string
	metric string
	minute int64
}

func (s *sampleSet) add(host, metric string, minute time.Time, v float64) {
	if s.idx == nil {
		s.idx = map[sampleKey]int{}
	}
	k := sampleKey{host: host, metric: metric, minute: minute.Unix()}
	if i, ok := s.idx[k]; ok {
		s.list[i].Value = v
		return
	}
	s.idx[k] = len(s.list)
	s.list = append(s.list, Sample{Host: host, Metric: metric, TS: minute, Value: v})
}
