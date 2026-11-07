package oomwatch

import (
	"time"
)

const oomReason = "OOMKilled"

// kill is one OOMKilled termination of one container.
type kill struct {
	Namespace    string
	Pod          string
	Container    string
	MemoryLimit  string
	RestartCount int
	ExitCode     int
	FinishedAt   time.Time
	id           string
}

// kills returns the OOMKilled terminations visible in the pod list. A
// container shows one in state (still down, for instance in back-off) or in
// lastState (already restarted); both count, and one termination is never
// listed twice.
func kills(pods []pod) []kill {
	var out []kill
	for _, p := range pods {
		limits := limitsByContainer(p)
		for _, group := range [][]containerStatus{p.Status.ContainerStatuses, p.Status.InitContainerStatuses} {
			for _, cs := range group {
				for _, t := range []*terminated{cs.LastState.Terminated, cs.State.Terminated} {
					if t == nil || t.Reason != oomReason || t.FinishedAt.IsZero() {
						continue
					}
					k := kill{
						Namespace:    p.Metadata.Namespace,
						Pod:          p.Metadata.Name,
						Container:    cs.Name,
						MemoryLimit:  limits[cs.Name],
						RestartCount: cs.RestartCount,
						ExitCode:     t.ExitCode,
						FinishedAt:   t.FinishedAt.UTC(),
					}
					k.id = p.Metadata.UID + "/" + cs.Name + "/" + k.FinishedAt.Format(time.RFC3339)
					out = append(out, k)
				}
			}
		}
	}
	return out
}

func limitsByContainer(p pod) map[string]string {
	limits := map[string]string{}
	for _, group := range [][]containerSpec{p.Spec.Containers, p.Spec.InitContainers} {
		for _, c := range group {
			limits[c.Name] = c.Resources.Limits["memory"]
		}
	}
	return limits
}

// tracker remembers which terminations were reported. A termination older than
// the window when the watcher first sees it is history from before it started
// and stays unreported; the window covers a watcher restart.
type tracker struct {
	seen   map[string]time.Time
	cutoff time.Time
}

func newTracker(start time.Time, lookback time.Duration) *tracker {
	return &tracker{seen: map[string]time.Time{}, cutoff: start.Add(-lookback)}
}

// fresh returns the kills not reported before and marks them reported. Entries
// for terminations that left the pod list are dropped, so the map follows the
// cluster instead of growing.
func (t *tracker) fresh(all []kill) []kill {
	var out []kill
	live := map[string]time.Time{}
	for _, k := range all {
		live[k.id] = k.FinishedAt
		if _, ok := t.seen[k.id]; ok {
			continue
		}
		t.seen[k.id] = k.FinishedAt
		if k.FinishedAt.Before(t.cutoff) {
			continue
		}
		out = append(out, k)
	}
	for id := range t.seen {
		if _, ok := live[id]; !ok {
			delete(t.seen, id)
		}
	}
	return out
}

// unmark forgets a kill whose report failed, so the next scan retries it.
func (t *tracker) unmark(k kill) { delete(t.seen, k.id) }
