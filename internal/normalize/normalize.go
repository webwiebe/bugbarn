package normalize

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/privacy"
)

func Normalize(raw []byte, ingestID string, receivedAt time.Time) (event.Event, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return event.Event{}, err
	}

	scrubbed, ok := privacy.Scrub(payload).(map[string]any)
	if !ok {
		return event.Event{}, errors.New("scrubbed payload is not an object")
	}

	observedAt := parseTime(firstString(payload, "observedTimestamp", "timestamp"))
	if observedAt.IsZero() {
		observedAt = receivedAt
	}

	evt := event.Event{
		IngestID:    ingestID,
		ReceivedAt:  receivedAt,
		ObservedAt:  observedAt,
		Severity:    firstString(payload, "severityText", "level"),
		Message:     stringValue(payload["body"]),
		Resource:    objectValue(scrubbed["resource"]),
		Attributes:  mergeAttributes(scrubbed),
		TraceID:     stringValue(scrubbed["traceId"]),
		SpanID:      stringValue(scrubbed["spanId"]),
		SDKName:     sdkName(scrubbed),
		RawScrubbed: scrubbed,
	}

	if evt.Message == "" {
		evt.Message = stringValue(scrubbed["message"])
	}

	evt.Exception = normalizeException(scrubbed["exception"])
	if evt.Message == "" {
		evt.Message = evt.Exception.Message
	}
	if evt.Severity == "" {
		evt.Severity = "ERROR"
	}

	return evt, nil
}

func normalizeException(value any) event.Exception {
	obj := objectValue(value)
	if obj == nil {
		return event.Exception{}
	}

	message := firstString(obj, "message", "value")
	return event.Exception{
		Type:       firstString(obj, "type", "name"),
		Message:    message,
		Stacktrace: normalizeStacktrace(obj["stacktrace"]),
	}
}

func normalizeStacktrace(value any) []event.StackFrame {
	frames, ok := value.([]any)
	if !ok {
		return nil
	}

	out := make([]event.StackFrame, 0, len(frames))
	for _, frame := range frames {
		obj := objectValue(frame)
		if obj == nil {
			continue
		}
		out = append(out, event.StackFrame{
			Function: firstString(obj, "function", "functionName"),
			File:     firstString(obj, "file", "filename", "abs_path"),
			Line:     intValue(obj["line"]),
			Column:   intValue(obj["column"]),
			Module:   stringValue(obj["module"]),
		})
	}
	return out
}

func mergeAttributes(scrubbed map[string]any) map[string]any {
	attrs := objectValue(scrubbed["attributes"])
	if attrs == nil {
		attrs = map[string]any{}
	}

	if tags := objectValue(scrubbed["tags"]); tags != nil {
		attrs["tags"] = tags
	}
	if extra := objectValue(scrubbed["extra"]); extra != nil {
		attrs["extra"] = extra
	}

	return attrs
}

func sdkName(payload map[string]any) string {
	if sender := objectValue(payload["sender"]); sender != nil {
		if sdk := objectValue(sender["sdk"]); sdk != nil {
			if name := firstString(sdk, "name", "sdk.name"); name != "" {
				return name
			}
		}
		return firstString(sender, "sdk.name", "name")
	}
	return firstString(payload, "sdk")
}

func objectValue(value any) map[string]any {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return obj
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func parseTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
