package ingestproc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// PersistRelease decodes a spooled release marker and creates it. POST
// /api/v1/releases enqueues the marker so the create stays off the request
// path: the writer owns the single SQLite write connection. The body mirrors
// the JSON the API handler accepted; the project the handler resolved travels
// on the record, so it is not resolved again here.
//
// A decode failure is permanent for the record. A store failure is returned
// as is, and the caller classifies it with storage.IsTransient.
func (p *Processor) PersistRelease(ctx context.Context, record spool.Record) error {
	body, err := base64.StdEncoding.DecodeString(record.BodyBase64)
	if err != nil {
		return fmt.Errorf("decode release body: %w", err)
	}
	var req struct {
		Name        string `json:"name"`
		Environment string `json:"environment"`
		ObservedAt  string `json:"observedAt"`
		Version     string `json:"version"`
		CommitSHA   string `json:"commitSha"`
		URL         string `json:"url"`
		Notes       string `json:"notes"`
		CreatedBy   string `json:"createdBy"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("unmarshal release: %w", err)
	}
	release := domain.Release{
		Name:        req.Name,
		Environment: req.Environment,
		Version:     req.Version,
		CommitSHA:   req.CommitSHA,
		URL:         req.URL,
		Notes:       req.Notes,
		CreatedBy:   req.CreatedBy,
	}
	if parsed, err := time.Parse(time.RFC3339Nano, req.ObservedAt); err == nil {
		release.ObservedAt = parsed
	}
	if record.ProjectID > 0 {
		ctx = domain.WithProjectID(ctx, record.ProjectID)
	}
	_, err = p.store.CreateRelease(ctx, release)
	return err
}
