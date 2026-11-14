package secnorm

import (
	"net"
	"strings"
)

// clientIP strips a port and returns the address only when it parses as an
// IP, so a hostname or garbage never lands in src_ip (rules group on it).
func clientIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	if ip := net.ParseIP(strings.Trim(s, "[]")); ip != nil {
		return ip.String()
	}
	return ""
}

// forwardedClient returns the client named by an X-Forwarded-For list, or ""
// when the header cannot be trusted. It is trusted only when peer, the address
// that connected to Traefik, is inside one of gateways. Each gateway appends
// the address it saw, so the list is read from the right, skipping gateways;
// the first other address is the client. Anything left of it was supplied by
// the client and is ignored.
func forwardedClient(xff, peer string, gateways []*net.IPNet) string {
	if len(gateways) == 0 || !inNets(peer, gateways) {
		return ""
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := clientIP(parts[i])
		if ip == "" {
			return ""
		}
		if !inNets(ip, gateways) {
			return ip
		}
	}
	return ""
}

func inNets(ip string, nets []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}
