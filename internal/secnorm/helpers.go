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

// firstForwarded returns the left-most address of an X-Forwarded-For list.
func firstForwarded(s string) string {
	first, _, _ := strings.Cut(s, ",")
	return clientIP(first)
}

// StatusString is Status for rule matching; empty when unset.
func (r Record) StatusString() string {
	if r.Status == 0 {
		return ""
	}
	return fmt.Sprint(r.Status)
}
