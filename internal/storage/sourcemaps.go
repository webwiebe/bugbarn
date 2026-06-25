package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

const sourceMapPrefix = "sourcemap-"

func (s *SourceMapStore) UploadSourceMap(ctx context.Context, upload SourceMapUpload) (SourceMap, error) {
	if strings.TrimSpace(upload.Release) == "" {
		return SourceMap{}, apperr.InvalidInput("source map release is required", nil)
	}
	if strings.TrimSpace(upload.BundleURL) == "" {
		return SourceMap{}, apperr.InvalidInput("source map bundle URL is required", nil)
	}
	now := time.Now().UTC()
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO source_maps (
	project_id,
	release,
	dist,
	bundle_url,
	name,
	content_type,
	source_map_blob,
	size_bytes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		upload.Release,
		upload.Dist,
		upload.BundleURL,
		upload.Name,
		upload.ContentType,
		upload.Blob,
		len(upload.Blob),
	)
	if err != nil {
		return SourceMap{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SourceMap{}, err
	}
	return SourceMap{
		ID:          formatID(sourceMapPrefix, id),
		Release:     upload.Release,
		Dist:        upload.Dist,
		BundleURL:   upload.BundleURL,
		Name:        upload.Name,
		ContentType: upload.ContentType,
		SizeBytes:   int64(len(upload.Blob)),
		UploadedAt:  now,
	}, nil
}

// FindSourceMap looks up the raw source map blob for the given release, dist, and bundleURL.
// Returns nil, nil if no matching row is found.
func (s *SourceMapStore) FindSourceMap(ctx context.Context, release, dist, bundleURL string) ([]byte, error) {
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}
	var blob []byte
	err := s.readDB().QueryRowContext(ctx, `
SELECT source_map_blob
FROM source_maps
WHERE project_id = ? AND release = ? AND dist = ? AND bundle_url = ?
ORDER BY id DESC
LIMIT 1`,
		projectID,
		release,
		dist,
		bundleURL,
	).Scan(&blob)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return blob, nil
}

// ListSourceMaps returns metadata for all source maps in the project (no blob).
func (s *SourceMapStore) ListSourceMaps(ctx context.Context) ([]SourceMapMeta, error) {
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}
	rows, err := s.readDB().QueryContext(ctx, `
SELECT id, release, dist, bundle_url, name, size_bytes, created_at
FROM source_maps
WHERE project_id = ?
ORDER BY id DESC`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceMapMeta
	for rows.Next() {
		var item SourceMapMeta
		var id int64
		var createdAt string
		if err := rows.Scan(&id, &item.Release, &item.Dist, &item.BundleURL, &item.Name, &item.SizeBytes, &createdAt); err != nil {
			return nil, err
		}
		item.ID = formatID(sourceMapPrefix, id)
		item.CreatedAt, _ = parseTime(createdAt)
		out = append(out, item)
	}
	return out, rows.Err()
}
