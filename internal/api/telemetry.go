package api

import (
	"encoding/json"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

type clientSpan struct {
	TraceID      string         `json:"traceId"`
	SpanID       string         `json:"spanId"`
	ParentSpanID string         `json:"parentSpanId,omitempty"`
	Name         string         `json:"name"`
	Service      string         `json:"service"`
	Kind         string         `json:"kind"`
	Status       string         `json:"status"`
	StartTime    int64          `json:"startTime"` // microseconds
	Duration     int64          `json:"duration"`  // microseconds
	Attributes   map[string]any `json:"attributes,omitempty"`
}

func (s *Server) serveTelemetry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spans []clientSpan `json:"spans"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tracer := tracing.Tracer()

	for _, cs := range req.Spans {
		tid, err := trace.TraceIDFromHex(cs.TraceID)
		if err != nil {
			continue
		}
		sid, err := trace.SpanIDFromHex(cs.SpanID)
		if err != nil {
			continue
		}

		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    tid,
			SpanID:     sid,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})

		if cs.ParentSpanID != "" {
			if psid, err := trace.SpanIDFromHex(cs.ParentSpanID); err == nil {
				sc = trace.NewSpanContext(trace.SpanContextConfig{
					TraceID:    tid,
					SpanID:     psid,
					TraceFlags: trace.FlagsSampled,
					Remote:     true,
				})
			}
		}

		parentCtx := trace.ContextWithRemoteSpanContext(ctx, sc)

		kind := trace.SpanKindClient
		switch cs.Kind {
		case "SERVER":
			kind = trace.SpanKindServer
		case "INTERNAL":
			kind = trace.SpanKindInternal
		case "PRODUCER":
			kind = trace.SpanKindProducer
		case "CONSUMER":
			kind = trace.SpanKindConsumer
		}

		startTime := time.UnixMicro(cs.StartTime)
		_, span := tracer.Start(parentCtx, cs.Name,
			trace.WithSpanKind(kind),
			trace.WithTimestamp(startTime),
		)

		for k, v := range cs.Attributes {
			switch val := v.(type) {
			case string:
				span.SetAttributes(attribute.String(k, val))
			case float64:
				span.SetAttributes(attribute.Float64(k, val))
			case bool:
				span.SetAttributes(attribute.Bool(k, val))
			}
		}

		if cs.Status == "ERROR" {
			span.SetStatus(codes.Error, "client error")
		}

		span.End(trace.WithTimestamp(startTime.Add(time.Duration(cs.Duration) * time.Microsecond)))
	}

	writeJSON(w, map[string]any{"ok": true, "accepted": len(req.Spans)})
}

func (s *Server) serveClientError(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Stack   string `json:"stack"`
		URL     string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	s.logger.ErrorContext(r.Context(), "client error",
		"error.message", req.Message,
		"error.type", req.Type,
		"error.stack", req.Stack,
		"error.url", req.URL,
	)

	writeJSON(w, map[string]any{"ok": true})
}
