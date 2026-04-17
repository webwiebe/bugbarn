package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
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

type Snapshot struct {
	Material    string
	Explanation []string
}

type material struct {
	ExceptionType string            `json:"exceptionType,omitempty"`
	Message       string            `json:"message,omitempty"`
	Stacktrace    []string          `json:"stacktrace,omitempty"`
	Context       map[string]string `json:"context,omitempty"`
}

var stableContextKeys = []string{
	"environment",
	"host",
	"http.method",
	"http.route",
	"http.status_code",
	"region",
	"release",
	"route",
	"service.name",
	"service.namespace",
	"status_code",
	"user_agent.family",
	"version",
}

func Fingerprint(evt event.Event) string {
	snapshot := SnapshotFor(evt)
	sum := sha256.Sum256([]byte(snapshot.Material))
	return hex.EncodeToString(sum[:])
}

func Material(evt event.Event) string {
	return SnapshotFor(evt).Material
}

func Explanation(evt event.Event) []string {
	return SnapshotFor(evt).Explanation
}

func SnapshotFor(evt event.Event) Snapshot {
	normalizedExceptionType := normalize(evt.Exception.Type)
	normalizedMessage := normalize(evt.Exception.Message)
	if normalizedMessage == "" {
		normalizedMessage = normalize(evt.Message)
	}

	frameParts := make([]string, 0, len(evt.Exception.Stacktrace))
	explanation := make([]string, 0, 8+len(evt.Exception.Stacktrace))

	if normalizedExceptionType != "" {
		explanation = append(explanation, "exception.type="+normalizedExceptionType)
	}
	if normalizedMessage != "" {
		explanation = append(explanation, "exception.message="+normalizedMessage)
	}

	for _, frame := range evt.Exception.Stacktrace {
		parts := []string{
			normalize(frame.Module),
			normalize(frame.Function),
			normalizePath(frame.File),
		}
		frameValue := strings.Join(trimEmpty(parts), ":")
		if frameValue != "" {
			frameParts = append(frameParts, frameValue)
			explanation = append(explanation, "stacktrace="+frameValue)
		}
	}

	context := stableContext(evt)
	for _, key := range sortedKeys(context) {
		explanation = append(explanation, "context."+key+"="+context[key])
	}

	payload := material{
		ExceptionType: normalizedExceptionType,
		Message:       normalizedMessage,
		Stacktrace:    frameParts,
		Context:       context,
	}
	materialJSON, _ := json.Marshal(payload)

	return Snapshot{
		Material:    string(materialJSON),
		Explanation: explanation,
	}
}

func stableContext(evt event.Event) map[string]string {
	fields := flattenScalarFields(evt.Resource)
	mergeFields(fields, flattenScalarFields(evt.Attributes))

	out := make(map[string]string)
	for _, key := range sortedKeys(fields) {
		normalizedKey := normalize(key)
		if !isStableContextKey(normalizedKey) {
			continue
		}
		if value := normalize(fields[key]); value != "" {
			out[normalizedKey] = value
		}
	}
	return out
}

func isStableContextKey(key string) bool {
	for _, stable := range stableContextKeys {
		if key == stable || strings.HasSuffix(key, "."+stable) || strings.HasSuffix(key, "/"+stable) {
			return true
		}
	}
	return false
}

func flattenScalarFields(value any) map[string]string {
	out := make(map[string]string)
	flattenScalarFieldsInto(out, "", value)
	return out
}

func flattenScalarFieldsInto(out map[string]string, prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenScalarFieldsInto(out, next, nested)
		}
	case []any:
		for idx, nested := range typed {
			next := prefix + "[" + strconv.Itoa(idx) + "]"
			flattenScalarFieldsInto(out, next, nested)
		}
	case string:
		if strings.TrimSpace(prefix) != "" {
			out[prefix] = typed
		}
	case bool:
		if strings.TrimSpace(prefix) != "" {
			out[prefix] = strconv.FormatBool(typed)
		}
	case float64:
		if strings.TrimSpace(prefix) != "" {
			out[prefix] = strconv.FormatFloat(typed, 'f', -1, 64)
		}
	case int:
		if strings.TrimSpace(prefix) != "" {
			out[prefix] = strconv.Itoa(typed)
		}
	case int64:
		if strings.TrimSpace(prefix) != "" {
			out[prefix] = strconv.FormatInt(typed, 10)
		}
	case json.Number:
		if strings.TrimSpace(prefix) != "" {
			out[prefix] = typed.String()
		}
	}
}

func mergeFields(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func trimEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, value)
	}
	return out
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
