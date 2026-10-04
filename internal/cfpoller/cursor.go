package cfpoller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

// Paging scheme
//
// Cloudflare stamps firewall events with a datetime of one-second precision,
// so several events often share a timestamp, and a page can end halfway
// through such a second. Advancing with datetime_gt past the last row would
// lose the rest of that second; re-reading it with datetime_geq would store
// its first rows twice.
//
// The cursor therefore holds the last timestamp read (TS) plus the keys of
// every event already stored at exactly that timestamp (Seen). Each query
// asks for datetime_geq TS, and rows at TS whose key is in Seen are dropped.
// A key is a hash of the event's full JSON plus its occurrence number within
// the result, so two byte-identical events at one second stay two rows.
//
// When more events share one second than fit on a page, the same page comes
// back on every query. The poller then reads that second alone with
// MaxQueryLimit and moves on to the next second; only when that query fills
// too are events lost, and the poller reports it.

// cursor is one zone's position, stored as JSON in the telemetrydb meta table.
type cursor struct {
	TS   time.Time `json:"ts"`
	Seen []string  `json:"seen,omitempty"`
}

func parseCursor(v string) (cursor, bool) {
	if v == "" {
		return cursor{}, false
	}
	var c cursor
	if err := json.Unmarshal([]byte(v), &c); err != nil || c.TS.IsZero() {
		return cursor{}, false
	}
	return c, true
}

func (c cursor) encode() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// eventTime is a node's datetime; ok is false when it is missing or malformed.
func eventTime(node map[string]any) (time.Time, bool) {
	s, _ := node["datetime"].(string)
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// page is the outcome of filtering one query result against a cursor.
type page struct {
	fresh []map[string]any // events not stored before, in order
	next  cursor           // cursor after storing fresh
}

// apply drops the rows of nodes that cur says are already stored and
// computes the cursor that follows them. nodes must be in datetime order.
func apply(cur cursor, nodes []map[string]any) page {
	seen := make(map[string]bool, len(cur.Seen))
	for _, k := range cur.Seen {
		seen[k] = true
	}
	next := cursor{TS: cur.TS, Seen: append([]string(nil), cur.Seen...)}
	occurrences := map[string]int{}
	var fresh []map[string]any
	for _, n := range nodes {
		ts, ok := eventTime(n)
		if !ok {
			// Without a datetime the event cannot be placed against the
			// cursor; keep it once and let it not move the cursor.
			fresh = append(fresh, n)
			continue
		}
		key := eventKey(n, occurrences)
		if ts.Equal(cur.TS) && seen[key] {
			continue
		}
		fresh = append(fresh, n)
		if ts.After(next.TS) {
			next = cursor{TS: ts}
		}
		if ts.Equal(next.TS) {
			next.Seen = append(next.Seen, key)
		}
	}
	return page{fresh: fresh, next: next}
}

// eventKey hashes the node's JSON (encoding/json sorts map keys, so equal
// nodes hash equal) and appends how often that hash occurred so far.
func eventKey(node map[string]any, occurrences map[string]int) string {
	b, _ := json.Marshal(node)
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:12])
	n := occurrences[h]
	occurrences[h] = n + 1
	return h + "#" + strconv.Itoa(n)
}
