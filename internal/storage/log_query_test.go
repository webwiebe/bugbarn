package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

var explorerBase = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// seedExplorer writes 8 entries across two hours: three services, two request
// ids, levels info/warn/error, one entry with a dotted key and one with no data.
func seedExplorer(t *testing.T, store *Store) int64 {
	t.Helper()
	pid := store.defaultProjectID
	at := func(min int) time.Time { return explorerBase.Add(time.Duration(min) * time.Minute) }
	entries := []LogEntry{
		{ProjectID: pid, ReceivedAt: at(1), LevelNum: 30, Level: "info", Message: "boot", Data: map[string]any{"service": "api", "region": "eu"}},
		{ProjectID: pid, ReceivedAt: at(2), LevelNum: 30, Level: "info", Message: "request ok", Data: map[string]any{"service": "api", "status": float64(200)}},
		{ProjectID: pid, ReceivedAt: at(3), LevelNum: 40, Level: "warn", Message: "slow request", Data: map[string]any{"service": "api", "status": float64(200)}},
		{ProjectID: pid, ReceivedAt: at(4), LevelNum: 50, Level: "error", Message: "request failed", Data: map[string]any{"service": "worker", "status": float64(500)}},
		{ProjectID: pid, ReceivedAt: at(5), LevelNum: 50, Level: "error", Message: "job failed", Data: map[string]any{"service": "worker", "http.code": "502"}},
		{ProjectID: pid, ReceivedAt: at(61), LevelNum: 30, Level: "info", Message: "second hour", Data: map[string]any{"service": "web", "ok": true}},
		{ProjectID: pid, ReceivedAt: at(62), LevelNum: 30, Level: "info", Message: "no data"},
		{ProjectID: pid, ReceivedAt: at(119), LevelNum: 50, Level: "error", Message: "late error", Data: map[string]any{"service": "web"}},
	}
	if err := store.InsertLogEntries(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	return pid
}

func messages(entries []LogEntry) string {
	var m []string
	for _, e := range entries {
		m = append(m, e.Message)
	}
	return strings.Join(m, ",")
}

func TestQueryLogEntriesFiltersByAttributesAndRange(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	pid := seedExplorer(t, store)
	ctx := context.Background()

	cases := []struct {
		name string
		q    LogQuery
		want string
	}{
		{"one attribute", LogQuery{ProjectID: pid, Attrs: []LogAttrFilter{{"service", "worker"}}}, "job failed,request failed"},
		{"attributes are ANDed", LogQuery{ProjectID: pid, Attrs: []LogAttrFilter{{"service", "api"}, {"status", "200"}}}, "slow request,request ok"},
		{"number matches as text", LogQuery{ProjectID: pid, Attrs: []LogAttrFilter{{"status", "500"}}}, "request failed"},
		{"dotted key", LogQuery{ProjectID: pid, Attrs: []LogAttrFilter{{"http.code", "502"}}}, "job failed"},
		{"no match", LogQuery{ProjectID: pid, Attrs: []LogAttrFilter{{"service", "missing"}}}, ""},
		{"time range is half open", LogQuery{ProjectID: pid, From: explorerBase.Add(2 * time.Minute), To: explorerBase.Add(4 * time.Minute)}, "slow request,request ok"},
		{"range plus level plus text", LogQuery{ProjectID: pid, LevelMin: 40, Q: "request", From: explorerBase, To: explorerBase.Add(time.Hour)}, "request failed,slow request"},
		{"attribute plus range", LogQuery{ProjectID: pid, Attrs: []LogAttrFilter{{"service", "web"}}, From: explorerBase.Add(time.Hour)}, "late error,second hour"},
	}
	for _, tc := range cases {
		got, err := store.QueryLogEntries(ctx, tc.q, 100)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if messages(got) != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, messages(got), tc.want)
		}
	}
}

func TestQueryLogEntriesRejectsBadAttributeFilters(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	seedExplorer(t, store)
	ctx := context.Background()

	tooMany := make([]LogAttrFilter, logAttrMax+1)
	for i := range tooMany {
		tooMany[i] = LogAttrFilter{Key: fmt.Sprintf("k%d", i), Value: "v"}
	}
	for name, attrs := range map[string][]LogAttrFilter{
		"empty key": {{"", "v"}},
		"long key":  {{strings.Repeat("k", logAttrKeyMax+1), "v"}},
		"too many":  tooMany,
	} {
		if _, err := store.QueryLogEntries(ctx, LogQuery{Attrs: attrs}, 10); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A key that looks like SQL or a JSON path stays a literal name.
	got, err := store.QueryLogEntries(ctx, LogQuery{Attrs: []LogAttrFilter{{`a"') OR 1=1 --`, "x"}}}, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("hostile key = %v, %v", got, err)
	}
}

func TestLogHistogramCountsPerBucketAndLevel(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	pid := seedExplorer(t, store)

	h, err := store.LogHistogram(context.Background(), LogQuery{ProjectID: pid, From: explorerBase, To: explorerBase.Add(2 * time.Hour)}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Buckets) != 2 || h.BucketSeconds != 3600 {
		t.Fatalf("buckets = %d (%ds)", len(h.Buckets), h.BucketSeconds)
	}
	first, second := h.Buckets[0].Counts, h.Buckets[1].Counts
	if first["info"] != 2 || first["warn"] != 1 || first["error"] != 2 {
		t.Errorf("first hour = %v", first)
	}
	if second["info"] != 2 || second["error"] != 1 || second["warn"] != 0 {
		t.Errorf("second hour = %v", second)
	}
	if !h.Buckets[1].Start.Equal(explorerBase.Add(time.Hour)) {
		t.Errorf("second bucket starts %s", h.Buckets[1].Start)
	}
	if h.Truncated {
		t.Error("small database reported truncated")
	}

	// Filters apply, and empty buckets stay in the answer.
	h, err = store.LogHistogram(context.Background(), LogQuery{ProjectID: pid, Attrs: []LogAttrFilter{{"service", "worker"}}, From: explorerBase, To: explorerBase.Add(2 * time.Hour)}, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Buckets) != 4 || h.Buckets[0].Counts["error"] != 2 || len(h.Buckets[1].Counts)+len(h.Buckets[2].Counts)+len(h.Buckets[3].Counts) != 0 {
		t.Errorf("filtered buckets = %+v", h.Buckets)
	}

	for _, bad := range []LogQuery{{}, {From: explorerBase}, {From: explorerBase, To: explorerBase}} {
		if _, err := store.LogHistogram(context.Background(), bad, time.Minute); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestLogFacetsListKeysAndTopValues(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	pid := seedExplorer(t, store)
	ctx := context.Background()
	q := LogQuery{ProjectID: pid, From: explorerBase, To: explorerBase.Add(3 * time.Hour)}

	keys, err := store.LogAttributeKeys(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys.Items) == 0 || keys.Items[0].Value != "service" || keys.Items[0].Count != 7 {
		t.Fatalf("keys = %+v", keys.Items)
	}
	have := map[string]int{}
	for _, k := range keys.Items {
		have[k.Value] = k.Count
	}
	if have["status"] != 3 || have["http.code"] != 1 || have["region"] != 1 || have["ok"] != 1 {
		t.Errorf("key counts = %v", have)
	}

	vals, err := store.LogAttributeValues(ctx, q, "service")
	if err != nil {
		t.Fatal(err)
	}
	want := []LogFacetCount{{"api", 3}, {"web", 2}, {"worker", 2}}
	if fmt.Sprint(vals.Items) != fmt.Sprint(want) {
		t.Fatalf("values = %+v, want %+v", vals.Items, want)
	}

	// Values respect the other filters.
	q.Attrs = []LogAttrFilter{{"status", "200"}}
	vals, _ = store.LogAttributeValues(ctx, q, "service")
	if len(vals.Items) != 1 || vals.Items[0].Value != "api" || vals.Items[0].Count != 2 {
		t.Errorf("filtered values = %+v", vals.Items)
	}

	// A key nobody carries answers an empty list, and a hostile key is a name.
	for _, k := range []string{"absent", `x') OR 1=1 --`} {
		v, err := store.LogAttributeValues(ctx, q, k)
		if err != nil || len(v.Items) != 0 {
			t.Errorf("key %q = %+v, %v", k, v.Items, err)
		}
	}
	if _, err := store.LogAttributeValues(ctx, q, ""); err == nil {
		t.Error("empty key accepted")
	}
}

func TestLogFacetValuesCapAtTen(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	pid := store.defaultProjectID
	var entries []LogEntry
	for i := 0; i < 15; i++ {
		for n := 0; n <= i; n++ {
			entries = append(entries, LogEntry{ProjectID: pid, ReceivedAt: explorerBase, LevelNum: 30, Level: "info", Message: "m", Data: map[string]any{"user": fmt.Sprintf("u%02d", i)}})
		}
	}
	if err := store.InsertLogEntries(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	vals, err := store.LogAttributeValues(context.Background(), LogQuery{ProjectID: pid}, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(vals.Items) != 10 || vals.Items[0].Value != "u14" || vals.Items[0].Count != 15 || vals.Items[9].Value != "u05" {
		t.Fatalf("top ten = %+v", vals.Items)
	}
}

// windowTruncated says so when rows inside the range may sit below the scan window.
func TestLogWindowTruncated(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	pid := seedExplorer(t, store)
	ctx := context.Background()

	if cut, err := store.windowTruncated(ctx, 0, explorerBase); err != nil || cut {
		t.Fatalf("no window = %v, %v", cut, err)
	}
	// Window starting after row 3 (oldest scanned: minute 4): a range from minute 0 is cut, one from minute 10 is not.
	if cut, _ := store.windowTruncated(ctx, 3, explorerBase); !cut {
		t.Error("range older than the window start was not reported truncated")
	}
	if cut, _ := store.windowTruncated(ctx, 3, explorerBase.Add(10*time.Minute)); cut {
		t.Error("range newer than the window start reported truncated")
	}
	_ = pid
}

// The explorer reads stay on the primary key or the project index and never
// scan the table whole: the scan window is an id bound, and a project filter
// walks idx_log_entries_project_id.
func TestLogExplorerQueryPlans(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	seedExplorer(t, store)

	run := func(name string, q LogQuery, minID int64, wantAny ...string) {
		where, args := logQueryWhere(q, minID)
		plan := logPlanOf(t, store, logListSelect+where+` ORDER BY le.id DESC LIMIT ?`, append(args, 10)...)
		if strings.Contains(plan, "SCAN le") {
			t.Errorf("%s scans the log table: %s", name, plan)
		}
		for _, w := range wantAny {
			if strings.Contains(plan, w) {
				return
			}
		}
		t.Errorf("%s plan %q uses none of %v", name, plan, wantAny)
	}
	run("windowed all-project read", LogQuery{From: explorerBase}, 100, "USING INTEGER PRIMARY KEY")
	run("project read", LogQuery{ProjectID: 1, From: explorerBase}, 100, "idx_log_entries_project_id")
	run("project attribute read", LogQuery{ProjectID: 1, Attrs: []LogAttrFilter{{"service", "api"}}}, 100, "idx_log_entries_project_id")
}

func logPlanOf(t *testing.T, store *Store, query string, args ...any) string {
	t.Helper()
	rows, err := store.readDB().Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, " | ")
}
