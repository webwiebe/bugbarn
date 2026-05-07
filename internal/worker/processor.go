package worker

import (
	"context"
	"encoding/base64"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
	"github.com/wiebe-xyz/bugbarn/internal/normalize"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

type ProcessedEvent struct {
	Event                  event.Event `json:"event"`
	Fingerprint            string      `json:"fingerprint"`
	FingerprintMaterial    string      `json:"fingerprintMaterial"`
	FingerprintExplanation []string    `json:"fingerprintExplanation"`
}

func ProcessRecord(record spool.Record) (ProcessedEvent, error) {
	return ProcessRecordCtx(context.Background(), record)
}

func ProcessRecordCtx(ctx context.Context, record spool.Record) (ProcessedEvent, error) {
	ctx, span := tracing.Tracer().Start(ctx, "worker.Process",
		trace.WithAttributes(attribute.String("ingest_id", record.IngestID)),
	)
	defer span.End()

	_, decodeSpan := tracing.Tracer().Start(ctx, "worker.Decode")
	body, err := base64.StdEncoding.DecodeString(record.BodyBase64)
	if err != nil {
		decodeSpan.SetStatus(codes.Error, err.Error())
		decodeSpan.End()
		span.SetStatus(codes.Error, err.Error())
		return ProcessedEvent{}, fmt.Errorf("decode spool body: %w", err)
	}
	decodeSpan.SetAttributes(attribute.Int("body_size", len(body)))
	decodeSpan.End()

	_, normalizeSpan := tracing.Tracer().Start(ctx, "worker.Normalize")
	evt, err := normalize.Normalize(body, record.IngestID, record.ReceivedAt)
	if err != nil {
		normalizeSpan.SetStatus(codes.Error, err.Error())
		normalizeSpan.End()
		span.SetStatus(codes.Error, err.Error())
		return ProcessedEvent{}, fmt.Errorf("normalize event: %w", err)
	}
	normalizeSpan.End()

	_, fpSpan := tracing.Tracer().Start(ctx, "worker.Fingerprint")
	fp := fingerprint.Fingerprint(evt)
	material := fingerprint.Material(evt)
	explanation := fingerprint.Explanation(evt)
	evt.Fingerprint = fp
	evt.FingerprintMaterial = material
	evt.FingerprintExplanation = explanation
	fpSpan.SetAttributes(attribute.String("fingerprint", fp))
	fpSpan.End()

	span.SetAttributes(
		attribute.String("fingerprint", fp),
		attribute.String("event.severity", evt.Severity),
	)

	return ProcessedEvent{
		Event:                  evt,
		Fingerprint:            fp,
		FingerprintMaterial:    material,
		FingerprintExplanation: explanation,
	}, nil
}

func ProcessRecords(records []spool.Record) ([]ProcessedEvent, error) {
	processed := make([]ProcessedEvent, 0, len(records))
	for _, record := range records {
		evt, err := ProcessRecord(record)
		if err != nil {
			return nil, err
		}
		processed = append(processed, evt)
	}
	return processed, nil
}
