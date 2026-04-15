package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

var (
	uuidPattern       = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	ipv4Pattern       = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	longNumber        = regexp.MustCompile(`\b\d{4,}\b`)
	hexAddress        = regexp.MustCompile(`(?i)\b0x[0-9a-f]{6,}\b`)
	redactedID        = regexp.MustCompile(`(?i)\[redacted-(?:id|ip|email|secret)\]`)
	whitespace        = regexp.MustCompile(`\s+`)
	pathNumberSegment = regexp.MustCompile(`/\d+`)
)

func Fingerprint(evt event.Event) string {
	material := Material(evt)
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:])
}

func Material(evt event.Event) string {
	parts := []string{
		normalize(evt.Exception.Type),
		normalize(evt.Exception.Message),
	}
	if parts[1] == "" {
		parts[1] = normalize(evt.Message)
	}

	for _, frame := range evt.Exception.Stacktrace {
		parts = append(parts,
			normalize(frame.Module),
			normalize(frame.Function),
			normalizePath(frame.File),
		)
	}

	return strings.Join(parts, "|")
}

func normalizePath(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	value = pathNumberSegment.ReplaceAllString(value, "/:num")
	return normalize(value)
}

func normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = uuidPattern.ReplaceAllString(value, "<id>")
	value = ipv4Pattern.ReplaceAllString(value, "<ip>")
	value = hexAddress.ReplaceAllString(value, "<hex>")
	value = longNumber.ReplaceAllString(value, "<num>")
	value = redactedID.ReplaceAllString(value, "<redacted>")
	value = whitespace.ReplaceAllString(value, " ")
	return value
}
