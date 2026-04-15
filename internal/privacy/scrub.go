package privacy

import (
	"regexp"
	"strings"
)

var (
	emailPattern = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	ipv4Pattern  = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	uuidPattern  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	tokenPattern = regexp.MustCompile(`(?i)\b(?:bearer|token|apikey|api_key|secret)=?[ :]+[a-z0-9._\-]{12,}\b`)
)

var sensitiveKeyParts = []string{
	"authorization",
	"cookie",
	"password",
	"passwd",
	"secret",
	"token",
	"api_key",
	"apikey",
	"session",
	"csrf",
	"email",
}

func Scrub(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if isSensitiveKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = Scrub(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = Scrub(child)
		}
		return out
	case string:
		return ScrubString(typed)
	default:
		return value
	}
}

func ScrubString(value string) string {
	value = emailPattern.ReplaceAllString(value, "[redacted-email]")
	value = ipv4Pattern.ReplaceAllString(value, "[redacted-ip]")
	value = uuidPattern.ReplaceAllString(value, "[redacted-id]")
	value = tokenPattern.ReplaceAllString(value, "[redacted-secret]")
	return value
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, part := range sensitiveKeyParts {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return false
}
