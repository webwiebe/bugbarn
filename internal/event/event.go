package event

import "time"

// UserContext holds identifying information about the user who triggered an event.
type UserContext struct {
	ID       string `json:"id,omitempty"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

// Breadcrumb records a single step in the application's execution leading up to an error.
type Breadcrumb struct {
	Timestamp string         `json:"timestamp"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Level     string         `json:"level,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type Event struct {
	IngestID               string         `json:"ingestId,omitempty"`
	ReceivedAt             time.Time      `json:"receivedAt,omitempty"`
	ObservedAt             time.Time      `json:"observedAt,omitempty"`
	Severity               string         `json:"severity,omitempty"`
	Message                string         `json:"message,omitempty"`
	Exception              Exception      `json:"exception,omitempty"`
	Resource               map[string]any `json:"resource,omitempty"`
	Attributes             map[string]any `json:"attributes,omitempty"`
	TraceID                string         `json:"traceId,omitempty"`
	SpanID                 string         `json:"spanId,omitempty"`
	SDKName                string         `json:"sdkName,omitempty"`
	Fingerprint            string         `json:"fingerprint,omitempty"`
	FingerprintMaterial    string         `json:"fingerprintMaterial,omitempty"`
	FingerprintExplanation []string       `json:"fingerprintExplanation,omitempty"`
	RawScrubbed            map[string]any `json:"rawScrubbed,omitempty"`
	User                   UserContext    `json:"user,omitempty"`
	Breadcrumbs            []Breadcrumb   `json:"breadcrumbs,omitempty"`
}

type Exception struct {
	Type       string       `json:"type,omitempty"`
	Message    string       `json:"message,omitempty"`
	Stacktrace []StackFrame `json:"stacktrace,omitempty"`
}

type StackFrame struct {
	Function string `json:"function,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Module   string `json:"module,omitempty"`

	// Original position fields populated by source map symbolication.
	OriginalFunction string `json:"originalFunction,omitempty"`
	OriginalFile     string `json:"originalFile,omitempty"`
	OriginalLine     int    `json:"originalLine,omitempty"`
	OriginalColumn   int    `json:"originalColumn,omitempty"`
	Snippet          string `json:"snippet,omitempty"`
}
