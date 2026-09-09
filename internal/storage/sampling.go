package storage

// Event sampling: how many copies of the same error we are willing to store.
//
// An issue is a fingerprint, so every event under it is by construction the same
// error. The first few hundred are evidence; the 500,000th is storage. Production
// carried 1,842,279 events for one fingerprint, and `events` is 94.7% of the
// database, so one client loop decides the size of the whole file.
//
// Past a threshold the write path keeps one event in K and records K on the row
// as its sample_weight, so counts still report real volume (see
// internal/storage/digest.go, issues_counts.go and projects.go, which all sum
// weights rather than counting rows). Nothing else about the issue changes:
// issues.event_count is incremented by the upsert before any of this and stays
// exact, and issues.representative_event_json means an issue always keeps a full
// example payload no matter how hard its events are sampled.

// samplingLadderSteps is how far K climbs. K goes 1 → 10 → 100 → 1000 → 10000,
// one rung per decade of events above the threshold, and then stops: at that
// point an issue stores one row per 10,000 occurrences and further rungs buy
// nothing a human would notice.
const samplingLadderSteps = 4

// defaultSampleAfter is the threshold below which every event is stored. It is
// deliberately generous — an issue has to be genuinely pathological before a
// human loses anything — and is overridable per deployment.
const defaultSampleAfter = 1000

// sampleRateFor returns K: how many events one stored row stands for once an
// issue has n of them. 1 means nothing is being sampled.
//
// The rung is chosen from n alone, so the decision needs no state, no query and
// no coordination between writers — the same n always makes the same choice. A
// non-positive keepAll disables sampling, which is what a caller passes for a
// project that has opted out.
func sampleRateFor(n, keepAll int64) int64 {
	if keepAll <= 0 || n <= keepAll {
		return 1
	}
	k := int64(1)
	bound := keepAll
	for step := 0; step < samplingLadderSteps; step++ {
		k *= 10
		bound *= 10
		if n <= bound {
			break
		}
	}
	return k
}

// sampleFor decides whether the n-th event of an issue is stored, and how many
// events the stored row stands for.
//
// n is the issue's lifetime event_count *after* the upsert incremented it, so
// the first event of an issue arrives here as n=1.
//
// A row is stored when n is a multiple of K, carrying K as its weight. Summing
// those weights therefore accounts for every event up to the last multiple of K
// and none of the ones after it, so a summed count trails the truth by less than
// one K and catches up the moment the next row lands. That undershoot is the
// only place these numbers are approximate; it is bounded by construction and it
// only ever affects the most recent events of an already-pathological issue.
func sampleFor(n, keepAll int64) (keep bool, weight int64) {
	k := sampleRateFor(n, keepAll)
	if n%k != 0 {
		return false, 0
	}
	return true, k
}
