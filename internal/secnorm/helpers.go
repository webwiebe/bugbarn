package secnorm

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// str reads a string field, rendering numbers so status codes and similar
// fields read the same whichever JSON type the shipper used.
func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func num(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func sub(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func has(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func firstMap(ms ...map[string]any) map[string]any {
	for _, m := range ms {
		if m != nil {
			return m
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

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

// StatusString is Status for rule matching; empty when unset.
func (r Record) StatusString() string {
	if r.Status == 0 {
		return ""
	}
	return fmt.Sprint(r.Status)
}
