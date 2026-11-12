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
	uuidPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	ipv4Pattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	// timestampPattern matches an ISO 8601 date, optionally followed by a time
	// (T or space separated, fractional seconds, Z or a UTC offset). The input is
	// already lowercased, so the separators are t and z.
	timestampPattern  = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[t ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:z|[+-]\d{2}(?::?\d{2})?)?)?\b`)
	hexAddress        = regexp.MustCompile(`(?i)\b0x[0-9a-f]{6,}\b`)
	hexToken          = regexp.MustCompile(`(?i)\b[0-9a-f]{12,}\b`)
	longNumber        = regexp.MustCompile(`\b\d+\b`)
	redactedID        = regexp.MustCompile(`(?i)\[redacted-(?:id|ip|email|secret)\]`)
	sourceLocation    = regexp.MustCompile(`:\d+(?::\d+)+`)
	whitespace        = regexp.MustCompile(`\s+`)
	pathNumberSegment = regexp.MustCompile(`/\d+`)
	// urlPathPattern matches the path, query and fragment of an absolute URL in
	// free text. Scheme and host stay: a different host is a different failing
	// service, a different page on the same host is the same failure.
	urlPathPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^/\s?#"'<>()\[\]]+)[/?#][^\s"'<>()\[\]]*`)
)

// urlPathPlaceholder replaces the path and query of a URL in a message.
const urlPathPlaceholder = "/<path>"

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

// stableContextKeys is the allowlist of resource and attribute keys that enter
// the fingerprint, matched by exact name or by a ".key" / "/key" suffix. A key
// belongs here only when its values form a small, fixed set that tells two
// failures apart (method, route template, status code, service). Free-form or
// per-target values such as region, host, url, user or tenant stay out, because
// a client that reports one error per target would open an issue per target
// (#241). Everything else is still stored on the event and searchable.
var stableContextKeys = []string{
	"environment",
	"http.method",
	"http.route",
	"http.status_code",
	"route",
	"service.name",
	"service.namespace",
	"status_code",
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
	exceptionType, message := headline(evt)

	frameParts := make([]string, 0, len(evt.Exception.Stacktrace))
	explanation := make([]string, 0, 8+len(evt.Exception.Stacktrace))

	if exceptionType != "" {
		explanation = append(explanation, "exception.type="+exceptionType)
	}
	if message != "" {
		explanation = append(explanation, "exception.message="+message)
	}

	for _, frame := range evt.Exception.Stacktrace {
		if frameValue := frameKey(frame); frameValue != "" {
			frameParts = append(frameParts, frameValue)
			explanation = append(explanation, "stacktrace="+frameValue)
		}
	}

	context := stableContext(evt)
	for _, key := range sortedKeys(context) {
		explanation = append(explanation, "context."+key+"="+context[key])
	}

	payload := material{
		ExceptionType: exceptionType,
		Message:       message,
		Stacktrace:    frameParts,
		Context:       context,
	}
	materialJSON, _ := json.Marshal(payload)

	return Snapshot{
		Material:    string(materialJSON),
		Explanation: explanation,
	}
}

// headline returns the normalised exception type and message. The message falls
// back to the event message. When the exception is empty but rawScrubbed has
// data, that is the fallback instead: it covers browser errors such as promise
// rejections and cross-origin errors that arrive with exception: {}.
func headline(evt event.Event) (exceptionType, message string) {
	exceptionType = normalize(evt.Exception.Type)
	message = normalizeMessage(evt.Exception.Message)
	if message == "" {
		message = normalizeMessage(evt.Message)
	}
	if exceptionType != "" || message != "" || len(evt.Exception.Stacktrace) != 0 {
		return exceptionType, message
	}
	raw := rawScrubbedFallback(evt.RawScrubbed)
	if raw.name == "" && raw.message == "" {
		return exceptionType, message
	}
	exceptionType = normalize(raw.name)
	message = normalizeMessage(raw.message)
	if message == "" {
		message = normalizeMessage(raw.source)
	}
	return exceptionType, message
}

// frameKey is one stack frame as "module:function:file", empty parts dropped.
func frameKey(frame event.StackFrame) string {
	return strings.Join(trimEmpty([]string{
		normalize(frame.Module),
		normalize(frame.Function),
		normalizePath(frame.File),
	}), ":")
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
		if value := normalizeContextValue(normalizedKey, fields[key]); value != "" {
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
	default:
		if text, ok := scalarText(value); ok && strings.TrimSpace(prefix) != "" {
			out[prefix] = text
		}
	}
}

// scalarText renders the scalar kinds the fingerprint reads; any other type is
// not a scalar and is skipped.
func scalarText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case json.Number:
		return typed.String(), true
	}
	return "", false
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

type rawScrubbedData struct {
	name    string
	message string
	source  string
}

func rawScrubbedFallback(rawScrubbed map[string]any) rawScrubbedData {
	if rawScrubbed == nil {
		return rawScrubbedData{}
	}
	name, _ := rawScrubbed["name"].(string)
	props, _ := rawScrubbed["properties"].(map[string]any)
	if props == nil {
		return rawScrubbedData{name: name}
	}
	message, _ := props["message"].(string)
	source, _ := props["source"].(string)
	if source == "" {
		source, _ = props["url"].(string)
	}
	return rawScrubbedData{name: name, message: message, source: source}
}

// normalizeMessage is normalize for free text that may carry a URL: the path
// and query of every absolute URL collapse, so a client that reports the same
// failure once per page lands in one issue. The title and the stored event keep
// the full URL; only the fingerprint sees the placeholder.
func normalizeMessage(value string) string {
	return normalize(urlPathPattern.ReplaceAllString(value, "$1"+urlPathPlaceholder))
}

// pathValueKeys are the context keys whose values are route templates
// ("/users/:id"): low cardinality by construction, so they keep their slashes.
var pathValueKeys = map[string]bool{"http.route": true, "route": true}

// normalizeContextValue normalizes one allowlisted context value. A value that
// holds a path separator (a "service.name" of "scrape/lyon") or a URL is
// per-page data, not a category, and collapses to a placeholder so it cannot
// give every page its own issue.
func normalizeContextValue(key, value string) string {
	if pathValueKeys[key] || strings.HasSuffix(key, ".route") {
		return normalize(value)
	}
	value = normalizeMessage(value)
	if strings.Contains(value, "/") && !strings.Contains(value, "://") {
		return "<path>"
	}
	return value
}

func normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = uuidPattern.ReplaceAllString(value, "<id>")
	value = timestampPattern.ReplaceAllString(value, "<ts>")
	value = ipv4Pattern.ReplaceAllString(value, "<ip>")
	value = hexAddress.ReplaceAllString(value, "<hex>")
	value = hexToken.ReplaceAllString(value, "<hex>")
	value = longNumber.ReplaceAllString(value, "<num>")
	value = sourceLocation.ReplaceAllString(value, ":<loc>")
	value = redactedID.ReplaceAllString(value, "<redacted>")
	value = whitespace.ReplaceAllString(value, " ")
	return value
}
