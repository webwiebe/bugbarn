package secnorm

import (
	"fmt"
	"net"
	"testing"
)

func nets(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func accessLine(peer, xff string) []byte {
	return []byte(fmt.Sprintf(`{"source":"traefik","message":"{\"ClientHost\":\"%s\",\"DownstreamStatus\":401,`+
		`\"RequestMethod\":\"GET\",\"RequestHost\":\"main.nijmegen.wiebe.xyz\",\"RequestPath\":\"/api/v1/contact\",`+
		`\"request_X-Forwarded-For\":\"%s\"}"}`, peer, xff))
}

func srcIP(t *testing.T, opts Options, peer, xff string) string {
	t.Helper()
	recs, _ := ParseWith(accessLine(peer, xff), now, opts)
	if len(recs) != 1 {
		t.Fatalf("got %d records", len(recs))
	}
	return recs[0].SrcIP
}

// Two clients behind the gateway must stay two sources: the rule groups on
// src_ip, and one gateway address would add their denials into one count.
func TestTwoClientsBehindGatewayStayApart(t *testing.T) {
	opts := Options{TrustedGateways: nets(t, "192.168.4.111/32")}
	a := srcIP(t, opts, "192.168.4.111", "198.51.100.1")
	b := srcIP(t, opts, "192.168.4.111", "198.51.100.2")
	if a != "198.51.100.1" || b != "198.51.100.2" {
		t.Fatalf("src_ip = %q and %q, want the two clients", a, b)
	}
}

func TestForwardedForIgnoredFromUntrustedPeer(t *testing.T) {
	opts := Options{TrustedGateways: nets(t, "192.168.4.111/32")}
	if got := srcIP(t, opts, "203.0.113.5", "198.51.100.1"); got != "203.0.113.5" {
		t.Fatalf("src_ip = %q, want the peer: a client can write any X-Forwarded-For", got)
	}
	if got := srcIP(t, Options{}, "192.168.4.111", "198.51.100.1"); got != "192.168.4.111" {
		t.Fatalf("src_ip = %q, want the peer when no gateway is configured", got)
	}
}

// A client that sends its own X-Forwarded-For puts addresses to the left of
// the one the gateway appended; the right-most non-gateway address wins.
func TestForwardedForSpoofedPrefixIgnored(t *testing.T) {
	opts := Options{TrustedGateways: nets(t, "192.168.4.111/32")}
	if got := srcIP(t, opts, "192.168.4.111", "1.2.3.4, 198.51.100.9"); got != "198.51.100.9" {
		t.Fatalf("src_ip = %q, want 198.51.100.9", got)
	}
	if got := srcIP(t, opts, "192.168.4.111", "garbage"); got != "192.168.4.111" {
		t.Fatalf("src_ip = %q, want the peer for an unparseable header", got)
	}
}

// A list made only of gateways names no client, and a peer that is not an
// address cannot be matched against the gateway ranges.
func TestForwardedForWithoutClient(t *testing.T) {
	opts := Options{TrustedGateways: nets(t, "192.168.4.0/24")}
	if got := srcIP(t, opts, "192.168.4.111", "192.168.4.5, 192.168.4.6"); got != "192.168.4.111" {
		t.Fatalf("src_ip = %q, want the peer when every listed address is a gateway", got)
	}
	if got := forwardedClient("198.51.100.1", "not-an-ip", opts.TrustedGateways); got != "" {
		t.Fatalf("forwardedClient = %q, want empty for an unparseable peer", got)
	}
}

func TestFieldRequestHost(t *testing.T) {
	r := Record{RequestHost: "main.nijmegen.wiebe.xyz"}
	if got, ok := r.Field("request_host"); !ok || got != "main.nijmegen.wiebe.xyz" {
		t.Fatalf("Field(request_host) = %q, %v", got, ok)
	}
}
