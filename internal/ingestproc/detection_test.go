package ingestproc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/detect"
)

// A detection goes through the same pipeline as an SDK event: it becomes one
// issue per rule and group in the telemetry project, repeats land on that
// issue, and the source IP survives scrubbing because the record is Internal.
func TestDetectionBecomesIssueWithSourceIP(t *testing.T) {
	t.Parallel()
	proc, store := newProcessor(t)
	ctx := context.Background()

	var brute detect.Rule
	for _, r := range detect.Defaults() {
		if r.ID == "ssh-bruteforce" {
			brute = r
		}
	}
	d := detect.Detection{
		Rule:  brute,
		Group: []detect.KV{{Key: "src_ip", Value: "203.0.113.9"}},
		At:    time.Now().UTC(), Count: 30, Limit: 30,
		Sample: "Failed password for root from 203.0.113.9 port 4022 ssh2",
	}
	persist := func() Result {
		rec, err := detect.Record(d, "infra")
		if err != nil {
			t.Fatal(err)
		}
		return proc.PersistRecord(ctx, rec)
	}

	first := persist()
	if first.Outcome != OutcomeSuccess {
		t.Fatalf("outcome = %v, err = %v", first.Outcome, first.Err)
	}
	if !first.IsNew || first.Issue.Fingerprint != detect.Fingerprint(d) {
		t.Fatalf("issue new=%v fingerprint=%q", first.IsNew, first.Issue.Fingerprint)
	}
	if !strings.Contains(first.Issue.Title, "203.0.113.9") {
		t.Errorf("title %q lost the source IP", first.Issue.Title)
	}
	attrs, _ := json.Marshal(first.Issue.RepresentativeEvent.Attributes)
	if !strings.Contains(string(attrs), "203.0.113.9") || strings.Contains(string(attrs), "redacted-ip") {
		t.Errorf("attributes scrubbed the source IP: %s", attrs)
	}

	second := persist()
	if second.IsNew || second.Issue.ID != first.Issue.ID {
		t.Fatalf("repeat detection made a new issue (%s vs %s)", second.Issue.ID, first.Issue.ID)
	}
	issues, err := store.ListIssues(projectCtx(t, ctx, store, "infra"))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("infra has %d issues, want 1", len(issues))
	}
}
