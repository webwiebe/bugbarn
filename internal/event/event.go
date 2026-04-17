package event

import "time"

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
}
