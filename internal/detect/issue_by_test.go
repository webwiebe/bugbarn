package detect

import (
	"context"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
)

func TestIssueByDayOneIssueForManyAddresses(t *testing.T) {
	r := rule(t, "ssh-bruteforce")
	if r.IssueBy != IssueByDay {
		t.Fatalf("ssh-bruteforce issue_by = %q", r.IssueBy)
	}
	e := testEngine(t, []Rule{r}, t0.Add(time.Hour))
	var batch []secnorm.Record
	for i := range 30 {
		at := t0.Add(time.Duration(i) * time.Second)
		batch = append(batch, sshFail("198.51.100.1", at), sshFail("198.51.100.2", at))
	}
	e.ObserveSecurity(context.Background(), batch)
	got := drain(e)
	if len(got) != 2 {
		t.Fatalf("two addresses over the threshold gave %d events, want 2", len(got))
	}
	if Fingerprint(got[0]) != Fingerprint(got[1]) {
		t.Errorf("fingerprints differ: %s vs %s", Fingerprint(got[0]), Fingerprint(got[1]))
	}
	if got[0].GroupKey() == got[1].GroupKey() {
		t.Errorf("each event should carry its own address, both have %s", got[0].GroupKey())
	}
	if title(got[0]) != title(got[1]) {
		t.Errorf("titles differ: %q vs %q", title(got[0]), title(got[1]))
	}
	next := got[0]
	next.At = next.At.Add(24 * time.Hour)
	if Fingerprint(next) == Fingerprint(got[0]) {
		t.Error("the next day must open a new issue")
	}
}

func TestHTTPAuthDeniedIssuesPerDay(t *testing.T) {
	if got := rule(t, "http-auth-denied").IssueBy; got != IssueByDay {
		t.Fatalf("http-auth-denied issue_by = %q", got)
	}
}

func TestIssueByRejectsUnknownValue(t *testing.T) {
	r := rule(t, "ssh-bruteforce")
	r.IssueBy = "week"
	if r.Validate() == nil {
		t.Error("issue_by week validated")
	}
}
