package detect

import (
	"container/list"
	"time"
)

// slots is the resolution of a sliding window: the window is split into this
// many buckets, so a count is exact to within one bucket (10s for 5m).
const slots = 30

// counter counts events per group in a sliding window of fixed-size buckets.
// Memory per group is constant whatever the event rate, which is what lets
// the engine keep tens of thousands of groups (one per source IP).
type counter struct {
	bucket    time.Duration
	head      int64 // newest bucket id seen
	ids       [slots]int64
	counts    [slots]uint32
	lastFired time.Time
}

func newCounter(window time.Duration) *counter {
	if window <= 0 {
		window = time.Second
	}
	b := window / slots
	if b <= 0 {
		b = 1
	}
	return &counter{bucket: b}
}

// add records one event at ts and returns the count within the window ending
// at the newest event seen. Events older than the window are ignored.
func (c *counter) add(ts time.Time) int {
	id := ts.UnixNano() / int64(c.bucket)
	if id > c.head {
		c.head = id
	}
	if id <= c.head-slots {
		return c.total()
	}
	i := id % slots
	if c.ids[i] != id {
		c.ids[i], c.counts[i] = id, 0
	}
	c.counts[i]++
	return c.total()
}

func (c *counter) total() int {
	n := 0
	for i := range c.ids {
		if c.ids[i] > c.head-slots && c.ids[i] <= c.head {
			n += int(c.counts[i])
		}
	}
	return n
}

// lru maps group keys to counters and drops the least recently used group
// once it holds max entries, so a scan from many addresses cannot grow memory
// without bound. A dropped group loses its count and its cooldown.
type lru struct {
	max   int
	order *list.List // front = most recently used
	items map[string]*list.Element
}

type lruEntry struct {
	key string
	c   *counter
}

func newLRU(maxEntries int) *lru {
	return &lru{max: maxEntries, order: list.New(), items: map[string]*list.Element{}}
}

func (l *lru) get(key string, window time.Duration) *counter {
	if el, ok := l.items[key]; ok {
		l.order.MoveToFront(el)
		return el.Value.(*lruEntry).c
	}
	c := newCounter(window)
	l.items[key] = l.order.PushFront(&lruEntry{key: key, c: c})
	if l.order.Len() > l.max {
		oldest := l.order.Back()
		l.order.Remove(oldest)
		delete(l.items, oldest.Value.(*lruEntry).key)
	}
	return c
}

func (l *lru) len() int { return l.order.Len() }

// reset drops every group, used when the rule set changes.
func (l *lru) reset() {
	l.order.Init()
	l.items = map[string]*list.Element{}
}
