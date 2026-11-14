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
	"github.com/wiebe-xyz/bugbarn/internal/privacy"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

func ProcessRecord(record spool.Record) (event.ProcessedEvent, error) {
	return ProcessRecordCtx(context.Background(), record)
}

func ProcessRecordCtx(ctx context.Context, record spool.Record) (event.ProcessedEvent, error) {
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
		return event.ProcessedEvent{}, fmt.Errorf("decode spool body: %w", err)
	}
	decodeSpan.SetAttributes(attribute.Int("body_size", len(body)))
	decodeSpan.End()

	_, normalizeSpan := tracing.Tracer().Start(ctx, "worker.Normalize")
	evt, err := normalize.NormalizeWith(body, record.IngestID, record.ReceivedAt, privacy.Options{KeepIPs: record.Internal})
	if err != nil {
		normalizeSpan.SetStatus(codes.Error, err.Error())
		normalizeSpan.End()
		span.SetStatus(codes.Error, err.Error())
		return event.ProcessedEvent{}, fmt.Errorf("normalize event: %w", err)
	}
	normalizeSpan.End()

	_, fpSpan := tracing.Tracer().Start(ctx, "worker.Fingerprint")
	fp := evt.Fingerprint
	if fp == "" {
		fp = fingerprint.Fingerprint(evt)
	}
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

	return event.ProcessedEvent{
		Event:                  evt,
		Fingerprint:            fp,
		FingerprintMaterial:    material,
		FingerprintExplanation: explanation,
	}, nil
}

func ProcessRecords(records []spool.Record) ([]event.ProcessedEvent, error) {
	processed := make([]event.ProcessedEvent, 0, len(records))
	for _, record := range records {
		evt, err := ProcessRecord(record)
		if err != nil {
			return nil, err
		}
		processed = append(processed, evt)
	}
	return processed, nil
}
