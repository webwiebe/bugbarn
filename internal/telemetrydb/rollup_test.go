package telemetrydb

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type hourRow struct {
	Host, Metric  string
	TS            int64
	Min, Avg, Max float64
	N             int
}

func insertSample(t *testing.T, d *DB, host, metric string, ts time.Time, v float64) {
	t.Helper()
	if _, err := d.Write().Exec(`INSERT INTO samples_1m (host, metric, ts, value) VALUES (?, ?, ?, ?)`,
		host, metric, ts.UnixMilli(), v); err != nil {
		t.Fatal(err)
	}
}

// seedHours writes one sample per minute for two hosts over `hours` hours
// starting at t0, with value = minute index within the hour (0..59), so each
// hour rolls up to min 0, avg 29.5, max 59, n 60.
func seedHours(t *testing.T, d *DB, hours int) {
	t.Helper()
	for _, host := range []string{"k3s1", "layer7"} {
		for m := 0; m < hours*60; m++ {
			insertSample(t, d, host, "load1", t0.Add(time.Duration(m)*time.Minute), float64(m%60))
		}
	}
}

func hourRows(t *testing.T, d *DB) []hourRow {
	t.Helper()
	rows, err := d.Read().Query(`SELECT host, metric, ts, min, avg, max, n FROM samples_1h ORDER BY host, metric, ts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []hourRow
	for rows.Next() {
		var r hourRow
		if err := rows.Scan(&r.Host, &r.Metric, &r.TS, &r.Min, &r.Avg, &r.Max, &r.N); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestRollupAggregatesFinishedHours(t *testing.T) {
	ctx := context.Background()
	d := openTest(t, Metrics, 0)
	seedHours(t, d, 3)
	// Samples on the exact boundaries belong to the hour they start.
	insertSample(t, d, "k3s1", "cpu.util", t0.Add(59*time.Minute), 10)
	insertSample(t, d, "k3s1", "cpu.util", t0.Add(time.Hour), 90)

	// Two hours and a bit later: hour 0 is finished, hour 1 is inside the
	// grace window, hour 2 is still running.
	now := t0.Add(2*time.Hour + rollupGrace - time.Second)
	n, err := d.Rollup(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("rollup = %d, %v; want 1 hour", n, err)
	}
	got := hourRows(t, d)
	want := []hourRow{
		{"k3s1", "cpu.util", t0.UnixMilli(), 10, 10, 10, 1},
		{"k3s1", "load1", t0.UnixMilli(), 0, 29.5, 59, 60},
		{"layer7", "load1", t0.UnixMilli(), 0, 29.5, 59, 60},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v\nwant %+v", got, want)
	}

	n, err = d.Rollup(ctx, t0.Add(3*time.Hour+rollupGrace))
	if err != nil || n != 2 {
		t.Fatalf("second rollup = %d, %v; want 2 hours", n, err)
	}
	if rows := hourRows(t, d); len(rows) != 8 {
		t.Fatalf("got %d hourly rows, want 8: %+v", len(rows), rows)
	}

	pts, err := d.Series(ctx, "k3s1", "cpu.util", t0, t0.Add(3*time.Hour), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 || pts[1].Value != 90 || pts[1].Min != 90 || !pts[1].TS.Equal(t0.Add(time.Hour)) {
		t.Fatalf("hourly series = %+v", pts)
	}
}

func TestRollupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	d := openTest(t, Metrics, 0)
	seedHours(t, d, 2)
	now := t0.Add(5 * time.Hour)
	if _, err := d.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	first := hourRows(t, d)
	n, err := d.Rollup(ctx, now)
	if err != nil || n != 0 {
		t.Fatalf("rerun rolled up %d hours, %v; want 0", n, err)
	}
	// Forcing the same hours again replaces their rows with identical values.
	if _, err := d.Write().Exec(`DELETE FROM meta`); err != nil {
		t.Fatal(err)
	}
	if n, err := d.Rollup(ctx, now); err != nil || n != 2 {
		t.Fatalf("forced rerun = %d, %v; want 2", n, err)
	}
	if again := hourRows(t, d); !reflect.DeepEqual(first, again) {
		t.Fatalf("rows changed on rerun:\n%+v\n%+v", first, again)
	}
}

func TestRollupResumesAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	d, err := Open(ctx, Metrics, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	seedHours(t, d, 4)
	now := t0.Add(6 * time.Hour)
	if n, err := d.rollup(ctx, now, 1); err != nil || n != 1 {
		t.Fatalf("partial rollup = %d, %v", n, err)
	}
	_ = d.Close()

	d, err = Open(ctx, Metrics, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if n, err := d.Rollup(ctx, now); err != nil || n != 3 {
		t.Fatalf("resumed rollup = %d, %v; want the 3 remaining hours", n, err)
	}
	ref := openTest(t, Metrics, 0)
	seedHours(t, ref, 4)
	if _, err := ref.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got, want := hourRows(t, d), hourRows(t, ref); !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed rows differ from an uninterrupted run:\n%+v\n%+v", got, want)
	}
}

func TestRollupSkipsEmptyHours(t *testing.T) {
	ctx := context.Background()
	d := openTest(t, Metrics, 0)
	insertSample(t, d, "k3s1", "load1", t0.Add(5*time.Minute), 1)
	insertSample(t, d, "k3s1", "load1", t0.Add(30*time.Hour+5*time.Minute), 3)
	n, err := d.Rollup(ctx, t0.Add(48*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("rollup = %d, %v; want the 2 hours that have samples", n, err)
	}
	rows := hourRows(t, d)
	if len(rows) != 2 || rows[1].TS != t0.Add(30*time.Hour).UnixMilli() {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestApplyRetentionBoundaries(t *testing.T) {
	ctx := context.Background()
	d := openTest(t, Metrics, 0)
	now := t0.Add(100 * 24 * time.Hour)
	ret := Retention{Raw: 7 * 24 * time.Hour, Hourly: 90 * 24 * time.Hour}
	rawCut, hourCut := now.Add(-ret.Raw), now.Add(-ret.Hourly)

	// More than one delete batch of expired minute rows.
	tx, err := d.Write().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < retentionBatch+500; i++ {
		if _, err := tx.Exec(`INSERT INTO samples_1m (host, metric, ts, value) VALUES (?, 'load1', ?, 1)`,
			fmt.Sprintf("h%d", i%7), rawCut.Add(-time.Duration(i+1)*time.Minute).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	insertSample(t, d, "k3s1", "load1", rawCut, 2) // exactly at the cutoff: kept
	for _, ts := range []time.Time{hourCut.Add(-time.Hour), hourCut} {
		if _, err := d.Write().Exec(`INSERT INTO samples_1h (host, metric, ts, min, avg, max, n)
			VALUES ('k3s1', 'load1', ?, 1, 1, 1, 60)`, ts.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	for host, seen := range map[string]time.Time{"gone": hourCut.Add(-time.Second), "edge": hourCut, "live": now} {
		if _, err := d.Write().Exec(`INSERT INTO hosts (host, last_seen) VALUES (?, ?)`, host, seen.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}

	if err := d.ApplyRetention(ctx, ret, now); err != nil {
		t.Fatal(err)
	}
	assertTS(t, d, `SELECT ts FROM samples_1m`, rawCut)
	assertTS(t, d, `SELECT ts FROM samples_1h`, hourCut)
	hosts, err := d.Hosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 || hosts[0].Host != "live" || hosts[1].Host != "edge" {
		t.Fatalf("hosts = %+v; want live and edge kept", hosts)
	}
}

func assertTS(t *testing.T, d *DB, q string, want ...time.Time) {
	t.Helper()
	rows, err := d.Read().Query(q + ` ORDER BY ts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			t.Fatal(err)
		}
		got = append(got, ts)
	}
	wantMs := make([]int64, len(want))
	for i, w := range want {
		wantMs[i] = w.UnixMilli()
	}
	if !reflect.DeepEqual(got, wantMs) {
		t.Fatalf("%s: ts = %v, want %v", q, got, wantMs)
	}
}

func TestApplyRetentionZeroWindowKeepsEverything(t *testing.T) {
	d := openTest(t, Metrics, 0)
	insertSample(t, d, "k3s1", "load1", t0, 1)
	if err := d.ApplyRetention(context.Background(), Retention{}, t0.Add(1000*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertTS(t, d, `SELECT ts FROM samples_1m`, t0)
}

func queryPlan(t *testing.T, d *DB, q string, args ...any) string {
	t.Helper()
	rows, err := d.Read().Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("explain %q: %v", q, err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	return strings.Join(plan, "; ")
}

// The rollup and retention statements share the single write connection with
// ingest, so each must walk an index. A plain "SCAN <table>" in any of these
// plans reads the whole table on every tick.
func TestRollupAndRetentionUseIndexes(t *testing.T) {
	d := openTest(t, Metrics, 0)
	cases := []struct {
		name, q string
		want    []string
	}{
		{"rollup insert", rollupHourSQL, []string{"SEARCH samples_1m USING INDEX idx_samples_1m_ts (ts>? AND ts<?)"}},
		{"next hour", `SELECT MIN(ts) FROM samples_1m WHERE ts >= ?`, []string{"USING COVERING INDEX idx_samples_1m_ts"}},
		{"cursor", `SELECT value FROM meta WHERE key = ?`, []string{"SEARCH meta USING PRIMARY KEY (key=?)"}},
		{"expire hosts", `DELETE FROM hosts WHERE last_seen < ?`, []string{"SEARCH hosts USING INDEX idx_hosts_last_seen (last_seen<?)"}},
	}
	for _, table := range []string{"samples_1m", "samples_1h"} {
		q := fmt.Sprintf(`DELETE FROM %[1]s WHERE (host, metric, ts) IN (
			SELECT host, metric, ts FROM %[1]s INDEXED BY idx_%[1]s_ts WHERE ts < ? ORDER BY ts LIMIT 10)`, table)
		cases = append(cases, struct {
			name, q string
			want    []string
		}{"expire " + table, q, []string{
			"SEARCH " + table + " USING PRIMARY KEY (host=? AND metric=? AND ts=?)",
			"SEARCH " + table + " USING COVERING INDEX idx_" + table + "_ts (ts<?)",
		}})
	}
	for _, c := range cases {
		args := make([]any, strings.Count(c.q, "?"))
		got := queryPlan(t, d, c.q, args...)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: plan %q, want %q", c.name, got, w)
			}
		}
		for _, table := range []string{"samples_1m", "samples_1h", "hosts", "meta"} {
			for _, step := range strings.Split(got, "; ") {
				if step == "SCAN "+table {
					t.Errorf("%s: full scan of %s in plan %q", c.name, table, got)
				}
			}
		}
	}
}
