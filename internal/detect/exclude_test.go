package detect

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
)

func deniedRec(ip, host string) secnorm.Record {
	return secnorm.Record{TS: t0, Source: "traefik", Status: 401, SrcIP: ip, RequestHost: host}
}

func excludeRule(t *testing.T) Rule {
	t.Helper()
	r := rule(t, "http-auth-denied")
	r.Threshold = 2
	r.Exclude = []Cond{
		{Field: "src_ip", Op: "in", Value: []any{"203.0.113.50"}},
		{Field: "request_host", Op: "suffix", Value: ".scanoo.nl"},
	}
	return r
}

func debugEngine(t *testing.T, r Rule) (*Engine, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	e := NewEngine([]Rule{r}, 100, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	e.now = func() time.Time { return t0.Add(time.Hour) }
	e.started = t0
	return e, &buf
}

func TestExcludedRecordsDoNotPageButStayVisible(t *testing.T) {
	e, buf := debugEngine(t, excludeRule(t))
	e.ObserveSecurity(context.Background(), []secnorm.Record{
		deniedRec("203.0.113.50", "f.scanoo.nl"), deniedRec("203.0.113.50", "bb.scanoo.nl"), // configured IP
		deniedRec("198.51.100.8", "sb.scanoo.nl"), deniedRec("198.51.100.8", "f.scanoo.nl"), // configured host suffix
	})
	if got := drain(e); len(got) != 0 {
		t.Fatalf("excluded records paged: %+v", got)
	}
	if n := strings.Count(buf.String(), "record excluded by rule"); n != 4 {
		t.Fatalf("excluded log lines = %d, want 4 (denials must stay visible)\n%s", n, buf.String())
	}
}

func TestExclusionDoesNotHideOtherDenials(t *testing.T) {
	e, _ := debugEngine(t, excludeRule(t))
	e.ObserveSecurity(context.Background(), []secnorm.Record{
		deniedRec("198.51.100.8", "bugbarn.wiebe.xyz"), deniedRec("198.51.100.8", "bugbarn.wiebe.xyz"),
	})
	if got := drain(e); len(got) != 1 || got[0].GroupKey() != "src_ip=198.51.100.8" {
		t.Fatalf("detections = %+v, want one for 198.51.100.8", got)
	}
}

func TestRuleWithoutExcludeIsUnchanged(t *testing.T) {
	r := rule(t, "http-auth-denied")
	r.Threshold = 2
	if len(r.Exclude) != 0 {
		t.Fatalf("defaults carry exclusions: %+v", r.Exclude)
	}
	e, _ := debugEngine(t, r)
	e.ObserveSecurity(context.Background(), []secnorm.Record{
		deniedRec("203.0.113.50", "f.scanoo.nl"), deniedRec("203.0.113.50", "f.scanoo.nl"),
	})
	if got := drain(e); len(got) != 1 {
		t.Fatalf("detections = %d, want 1 without an exclude list", len(got))
	}
}

func TestExcludeIsValidated(t *testing.T) {
	r := excludeRule(t)
	if err := r.Validate(); err != nil {
		t.Fatalf("valid exclude rejected: %v", err)
	}
	r.Exclude = []Cond{{Field: "nope", Op: "eq", Value: "x"}}
	if err := r.Validate(); err == nil {
		t.Fatal("exclude on an unknown field was accepted")
	}
	r.Exclude = []Cond{{Field: "src_ip", Op: "in", Value: "not-a-list"}}
	if err := r.Validate(); err == nil {
		t.Fatal("exclude with a bad value shape was accepted")
	}
}

// Two clients behind the same gateway must not add up to one source: with the
// gateway as the peer, each client's denials count on its own.
func TestClientsBehindGatewayAreNotOneSource(t *testing.T) {
	_, gw, _ := net.ParseCIDR("192.168.4.111/32")
	line := func(client string) string {
		return fmt.Sprintf(`{"source":"traefik","message":"{\"ClientHost\":\"192.168.4.111\",\"DownstreamStatus\":401,`+
			`\"RequestMethod\":\"GET\",\"RequestHost\":\"main.nijmegen.wiebe.xyz\",\"RequestPath\":\"/x\",`+
			`\"request_X-Forwarded-For\":\"%s\"}"}`, client)
	}
	body := []byte(line("198.51.100.1") + "\n" + line("198.51.100.2") + "\n")

	r := rule(t, "http-auth-denied")
	r.Threshold = 2 // one denial from each client
	recs, _ := secnorm.ParseWith(body, t0, secnorm.Options{TrustedGateways: []*net.IPNet{gw}})
	e := testEngine(t, []Rule{r}, t0.Add(time.Hour))
	e.ObserveSecurity(context.Background(), recs)
	if got := drain(e); len(got) != 0 {
		t.Fatalf("two clients behind the gateway added up to one source: %+v", got)
	}

	// Without the gateway configured they do (the gateway address is the source).
	recs, _ = secnorm.ParseWith(body, t0, secnorm.Options{})
	e = testEngine(t, []Rule{r}, t0.Add(time.Hour))
	e.ObserveSecurity(context.Background(), recs)
	if got := drain(e); len(got) != 1 || got[0].GroupKey() != "src_ip=192.168.4.111" {
		t.Fatalf("detections = %+v, want one for the gateway address", got)
	}
}
