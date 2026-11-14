package telemetryview

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

var fixedNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func openDB(t *testing.T, spec telemetrydb.Spec) *telemetrydb.DB {
	t.Helper()
	db, err := telemetrydb.Open(context.Background(), spec, filepath.Join(t.TempDir(), spec.Name+".db"), 0)
	if err != nil {
		t.Fatalf("open %s: %v", spec.Name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func serviceWith(t *testing.T, sec, met *telemetrydb.DB) (*Service, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	s := New(telemetrydb.Fixed(sec), telemetrydb.Fixed(met), logger)
	s.now = func() time.Time { return fixedNow }
	return s, &buf
}

func TestSearchSecurityReturnsRowsNewestFirstWithDefaultLimit(t *testing.T) {
	ctx := context.Background()
	sec := openDB(t, telemetrydb.Security)
	recs := []secnorm.Record{
		{TS: fixedNow.Add(-2 * time.Hour), Source: "sshd", Host: "k3s1", Kind: "auth_failure", SrcIP: "203.0.113.9", User: "root"},
		{TS: fixedNow.Add(-1 * time.Hour), Source: "traefik", Host: "k3s1", Kind: "http", SrcIP: "203.0.113.9", Status: 404, Path: "/x"},
		{TS: fixedNow.Add(-72 * time.Hour), Source: "sshd", Host: "k3s1", Kind: "auth_failure", SrcIP: "198.51.100.1"},
	}
	if err := sec.InsertSecurity(ctx, recs, fixedNow); err != nil {
		t.Fatalf("insert: %v", err)
	}
	s, _ := serviceWith(t, sec, nil)

	rows, err := s.SearchSecurity(ctx, telemetrydb.SecurityQuery{})
	if err != nil {
		t.Fatalf("SearchSecurity: %v", err)
	}
	if len(rows) != 2 || rows[0].Source != "traefik" || rows[1].Source != "sshd" {
		t.Fatalf("rows = %+v; want the two rows of the default 24h window, newest first", rows)
	}

	rows, err = s.SearchSecurity(ctx, telemetrydb.SecurityQuery{SrcIP: "203.0.113.9", Status: 404, Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].Path != "/x" {
		t.Fatalf("filtered rows = %+v, %v", rows, err)
	}
}

func TestMetricsReads(t *testing.T) {
	ctx := context.Background()
	met := openDB(t, telemetrydb.Metrics)
	ts := fixedNow.Add(-30 * time.Minute).Truncate(time.Minute)
	samples := []hostmetrics.Sample{
		{Host: "k3s1", Metric: "load1", TS: ts, Value: 1.5},
		{Host: "k3s1", Metric: "cpu.util", TS: ts, Value: 40},
	}
	if err := met.InsertMetrics(ctx, samples, []hostmetrics.HostInfo{{Host: "k3s1", Cores: 8, LastSeen: ts}}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	s, _ := serviceWith(t, nil, met)

	hosts, err := s.Hosts(ctx)
	if err != nil || len(hosts) != 1 || hosts[0].Host != "k3s1" || hosts[0].Cores != 8 {
		t.Fatalf("Hosts = %+v, %v", hosts, err)
	}
	names, err := s.HostMetrics(ctx, "k3s1")
	if err != nil || len(names) != 2 {
		t.Fatalf("HostMetrics = %v, %v", names, err)
	}
	series, err := s.Series(ctx, "k3s1", "load1", time.Time{}, time.Time{})
	if err != nil || series.Hourly || len(series.Points) != 1 || series.Points[0].Value != 1.5 {
		t.Fatalf("Series = %+v, %v", series, err)
	}
	long, err := s.Series(ctx, "k3s1", "load1", fixedNow.Add(-30*24*time.Hour), fixedNow)
	if err != nil || !long.Hourly {
		t.Fatalf("a 30 day range should read hourly rollups, got %+v, %v", long, err)
	}
}

func TestSeriesRejectsReversedWindow(t *testing.T) {
	s, _ := serviceWith(t, nil, nil)
	_, err := s.Series(context.Background(), "h", "m", fixedNow, fixedNow.Add(-time.Hour))
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
}

type errSource struct{ err error }

func (e errSource) DB(context.Context) (*telemetrydb.DB, error) { return nil, e.err }

func TestSourceFailuresMapToKindsAndLogLevels(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		err       error
		wantKind  error
		wantLevel string
	}{
		{"not created", telemetrydb.ErrNotCreated, apperr.ErrUnavailable, ""},
		{"unavailable", telemetrydb.ErrUnavailable, apperr.ErrUnavailable, ""},
		{"canceled", context.Canceled, nil, "INFO"},
		{"other", errors.New("disk"), nil, "ERROR"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		s := New(errSource{tc.err}, errSource{tc.err}, slog.New(slog.NewJSONHandler(&buf, nil)))
		calls := map[string]func() error{
			"hosts":    func() error { _, err := s.Hosts(ctx); return err },
			"metrics":  func() error { _, err := s.HostMetrics(ctx, "h"); return err },
			"series":   func() error { _, err := s.Series(ctx, "h", "m", time.Time{}, time.Time{}); return err },
			"security": func() error { _, err := s.SearchSecurity(ctx, telemetrydb.SecurityQuery{}); return err },
		}
		for name, call := range calls {
			buf.Reset()
			err := call()
			if tc.wantKind != nil && !errors.Is(err, tc.wantKind) {
				t.Errorf("%s/%s: err = %v, want kind %v", tc.name, name, err, tc.wantKind)
			}
			if tc.wantKind == nil && !errors.Is(err, tc.err) {
				t.Errorf("%s/%s: err = %v, want the source error", tc.name, name, err)
			}
			if tc.wantLevel == "" && buf.Len() != 0 {
				t.Errorf("%s/%s: unexpected log: %s", tc.name, name, buf.String())
			}
			if tc.wantLevel != "" && !strings.Contains(buf.String(), `"level":"`+tc.wantLevel+`"`) {
				t.Errorf("%s/%s: log = %s, want level %s", tc.name, name, buf.String(), tc.wantLevel)
			}
		}
	}
}

func TestQueryFailuresAfterTheFileOpenedAreLogged(t *testing.T) {
	ctx := context.Background()
	sec := openDB(t, telemetrydb.Security)
	met := openDB(t, telemetrydb.Metrics)
	s, buf := serviceWith(t, sec, met)
	if err := sec.Close(); err != nil {
		t.Fatal(err)
	}
	if err := met.Close(); err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"hosts":    func() error { _, err := s.Hosts(ctx); return err },
		"metrics":  func() error { _, err := s.HostMetrics(ctx, "h"); return err },
		"series":   func() error { _, err := s.Series(ctx, "h", "m", time.Time{}, time.Time{}); return err },
		"security": func() error { _, err := s.SearchSecurity(ctx, telemetrydb.SecurityQuery{}); return err },
	}
	for name, call := range calls {
		buf.Reset()
		if err := call(); err == nil {
			t.Errorf("%s: want an error from a closed database", name)
		}
		if !strings.Contains(buf.String(), `"level":"ERROR"`) {
			t.Errorf("%s: query failure not logged at error level: %s", name, buf.String())
		}
	}
}
