package detect

import (
	"context"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
)

func TestDiskFullSkipsPseudoMounts(t *testing.T) {
	e := testEngine(t, []Rule{rule(t, "disk-full")}, t0.Add(time.Hour))
	ctx := context.Background()
	for m := range 11 {
		at := t0.Add(time.Duration(m) * time.Minute)
		e.ObserveMetrics(ctx, []hostmetrics.Sample{
			sample("mac", "fs./Volumes/Recovery.used_pct", at, 95),
			sample("k3s1", "fs./etc/hosts.used_pct", at, 95),
			sample("k3s1", "fs./etc/vector.used_pct", at, 95),
			sample("k3s1", "fs./var/lib/vector.used_pct", at, 95),
			sample("k3s1", "fs./var/log.used_pct", at, 95),
			sample("k3s1", "fs./.used_pct", at, 95),
		}, nil)
	}
	got := drain(e)
	if len(got) != 1 || got[0].GroupKey() != "host=k3s1,metric=fs./.used_pct" {
		t.Fatalf("detections = %+v, want one for k3s1 /", got)
	}
}

func TestExcludeMetricRejectsEmptyEntry(t *testing.T) {
	r := rule(t, "disk-full")
	r.ExcludeMetric = []string{""}
	if r.Validate() == nil {
		t.Error("empty exclude_metric entry validated")
	}
}
