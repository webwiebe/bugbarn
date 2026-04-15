package worker

import (
	"encoding/base64"
	"fmt"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
	"github.com/wiebe-xyz/bugbarn/internal/normalize"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

type ProcessedEvent struct {
	Event       event.Event `json:"event"`
	Fingerprint string      `json:"fingerprint"`
}

func ProcessRecord(record spool.Record) (ProcessedEvent, error) {
	body, err := base64.StdEncoding.DecodeString(record.BodyBase64)
	if err != nil {
		return ProcessedEvent{}, fmt.Errorf("decode spool body: %w", err)
	}

	evt, err := normalize.Normalize(body, record.IngestID, record.ReceivedAt)
	if err != nil {
		return ProcessedEvent{}, fmt.Errorf("normalize event: %w", err)
	}

	return ProcessedEvent{
		Event:       evt,
		Fingerprint: fingerprint.Fingerprint(evt),
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
