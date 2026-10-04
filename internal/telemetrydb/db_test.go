package telemetrydb

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func openTest(t *testing.T, spec Spec, maxBytes int64) *DB {
	t.Helper()
	d, err := Open(context.Background(), spec, filepath.Join(t.TempDir(), spec.Name+".db"), maxBytes)
	if err != nil {
		t.Fatalf("open %s: %v", spec.Name, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestOpenSetsIncrementalAutoVacuum(t *testing.T) {
	for _, spec := range []Spec{Security, Metrics} {
		d := openTest(t, spec, 0)
		var mode int
		if err := d.Write().QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != 2 {
			t.Fatalf("%s: auto_vacuum = %d, want 2 (INCREMENTAL)", spec.Name, mode)
		}
		var journal string
		if err := d.Write().QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if journal != "wal" {
			t.Fatalf("%s: journal_mode = %q, want wal", spec.Name, journal)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.db")
	for i := 0; i < 2; i++ {
		d, err := Open(context.Background(), Security, path, 0)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		_ = d.Close()
	}
}

func TestOpenReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	if _, err := OpenReadOnly(context.Background(), Metrics, path); err == nil {
		t.Fatal("expected an error before the writer created the file")
	}
	w, err := Open(context.Background(), Metrics, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write().Exec(`INSERT INTO hosts (host, last_seen) VALUES ('k3s1', 1)`); err != nil {
		t.Fatal(err)
	}
	r, err := OpenReadOnly(context.Background(), Metrics, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Write() != nil {
		t.Fatal("read-only DB has a write pool")
	}
	var n int
	if err := r.Read().QueryRow(`SELECT COUNT(*) FROM hosts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hosts = %d, %v", n, err)
	}
	if _, err := r.Read().Exec(`INSERT INTO hosts (host, last_seen) VALUES ('x', 1)`); err == nil {
		t.Fatal("insert through the read-only pool succeeded")
	}
	if used, err := r.UsedBytes(context.Background()); err != nil || used <= 0 {
		t.Fatalf("UsedBytes on read-only = %d, %v", used, err)
	}
}

func fillSecurity(t *testing.T, d *DB, rows int) {
	t.Helper()
	raw := strings.Repeat("x", 1024)
	tx, err := d.Write().Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO security_logs (ts, received_at, source, raw) VALUES (?, ?, 'sshd', ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if _, err := stmt.Exec(i, i, raw); err != nil {
			t.Fatal(err)
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestEnforceCapEvictsOldestUnderTheBand(t *testing.T) {
	const capBytes = 4 << 20
	d := openTest(t, Security, capBytes)
	ctx := context.Background()
	fillSecurity(t, d, 12000) // ~13MB of rows, three times the cap.
	if err := d.EnforceCap(ctx); err != nil {
		t.Fatal(err)
	}
	used, err := d.UsedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if used > capBytes*evictStopPct/100 {
		t.Fatalf("used %d bytes after eviction, want <= %d", used, capBytes*evictStopPct/100)
	}
	if d.Full() {
		t.Fatal("Full still set after eviction")
	}
	var minID, maxID int64
	if err := d.Read().QueryRow(`SELECT MIN(id), MAX(id) FROM security_logs`).Scan(&minID, &maxID); err != nil {
		t.Fatal(err)
	}
	if maxID != 12000 || minID <= 1 {
		t.Fatalf("kept ids %d..%d; want the newest rows kept and the oldest gone", minID, maxID)
	}
	var free int64
	if err := d.Write().QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free != 0 {
		t.Fatalf("freelist_count = %d after vacuum, want 0", free)
	}
}

func TestEnforceCapLeavesSmallFilesAlone(t *testing.T) {
	d := openTest(t, Security, 64<<20)
	fillSecurity(t, d, 100)
	if err := d.EnforceCap(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.Read().QueryRow(`SELECT COUNT(*) FROM security_logs`).Scan(&n); err != nil || n != 100 {
		t.Fatalf("rows = %d, %v; want all 100 kept", n, err)
	}
}

func TestEnforceCapEvictsMetricsByTime(t *testing.T) {
	const capBytes = 1 << 20
	d := openTest(t, Metrics, capBytes)
	tx, err := d.Write().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40000; i++ {
		if _, err := tx.Exec(`INSERT INTO samples_1m (host, metric, ts, value) VALUES (?, 'load1', ?, 1)`,
			fmt.Sprintf("host-%d", i%10), i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := d.EnforceCap(context.Background()); err != nil {
		t.Fatal(err)
	}
	var minTS, maxTS int64
	if err := d.Read().QueryRow(`SELECT MIN(ts), MAX(ts) FROM samples_1m`).Scan(&minTS, &maxTS); err != nil {
		t.Fatalf("samples after eviction: %v (was the table emptied?)", err)
	}
	if minTS == 0 || maxTS != 39999 {
		t.Fatalf("kept ts %d..%d; want the oldest evicted and the newest kept", minTS, maxTS)
	}
}

// The eviction subqueries must walk an index; a table scan here would run on
// the shared write connection every time the file is near its cap.
func TestEvictionUsesAnIndex(t *testing.T) {
	// security_logs is ordered by its rowid alias, so a plain SCAN with no
	// sort is the index walk there.
	cases := map[string]Spec{"SCAN security_logs": Security, "USING COVERING INDEX idx_samples_1m_ts": Metrics}
	for want, spec := range cases {
		d := openTest(t, spec, 0)
		q := fmt.Sprintf(`EXPLAIN QUERY PLAN SELECT %[2]s FROM %[1]s ORDER BY %[2]s LIMIT 1 OFFSET 10`, spec.evictTable, spec.evictCol)
		rows, err := d.Read().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		got := strings.Join(plan, "; ")
		if !strings.Contains(got, want) || strings.Contains(got, "TEMP B-TREE") {
			t.Fatalf("%s: plan %q, want %q without a sort", spec.Name, got, want)
		}
	}
}

func TestRunMaintenanceStopsOnCancel(t *testing.T) {
	d := openTest(t, Security, 1<<30)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.RunMaintenance(ctx, 10*time.Millisecond, quiet()); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunMaintenance did not return after cancel")
	}
	d.FinalCheckpoint(quiet())
}
