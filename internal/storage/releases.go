package storage

import (
	"context"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

const releaseIDPrefix = "release-"

func (s *Store) ListReleases(ctx context.Context) ([]Release, error) {
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}

	rows, err := s.readDB().QueryContext(ctx, `
SELECT
	id,
	name,
	environment,
	observed_at,
	version,
	commit_sha,
	url,
	notes,
	created_by,
	created_at
FROM releases
WHERE project_id = ?
ORDER BY observed_at DESC, id DESC`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var releases []Release
	for rows.Next() {
		item, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		releases = append(releases, item)
	}
	return releases, rows.Err()
}

func (s *Store) GetRelease(ctx context.Context, releaseID string) (Release, error) {
	rowID, err := parseID(releaseIDPrefix, releaseID)
	if err != nil {
		return Release{}, apperr.InvalidInput("invalid release ID", err)
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}

	row := s.readDB().QueryRowContext(ctx, `
SELECT
	id,
	name,
	environment,
	observed_at,
	version,
	commit_sha,
	url,
	notes,
	created_by,
	created_at
FROM releases
WHERE project_id = ? AND id = ?`,
		projectID,
		rowID,
	)
	release, err := scanRelease(row)
	if err != nil {
		return Release{}, wrapNotFound(err, "release not found")
	}
	return release, nil
}

func (s *Store) CreateRelease(ctx context.Context, release Release) (Release, error) {
	if strings.TrimSpace(release.Name) == "" {
		return Release{}, apperr.InvalidInput("release name is required", nil)
	}
	if release.ObservedAt.IsZero() {
		release.ObservedAt = time.Now().UTC()
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}

	res, err := s.db.ExecContext(ctx, `
INSERT INTO releases (
	project_id,
	name,
	environment,
	observed_at,
	version,
	commit_sha,
	url,
	notes,
	created_by
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		release.Name,
		release.Environment,
		formatTime(release.ObservedAt),
		release.Version,
		release.CommitSHA,
		release.URL,
		release.Notes,
		release.CreatedBy,
	)
	if err != nil {
		return Release{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Release{}, err
	}
	release.ID = formatID(releaseIDPrefix, id)
	release.CreatedAt = time.Now().UTC()
	return release, nil
}

func (s *Store) UpdateRelease(ctx context.Context, releaseID string, release Release) (Release, error) {
	rowID, err := parseID(releaseIDPrefix, releaseID)
	if err != nil {
		return Release{}, apperr.InvalidInput("invalid release ID", err)
	}
	if strings.TrimSpace(release.Name) == "" {
		return Release{}, apperr.InvalidInput("release name is required", nil)
	}
	if release.ObservedAt.IsZero() {
		release.ObservedAt = time.Now().UTC()
	}

	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}

	if _, err := s.db.ExecContext(ctx, `
UPDATE releases
SET name = ?, environment = ?, observed_at = ?, version = ?, commit_sha = ?, url = ?, notes = ?, created_by = ?
WHERE project_id = ? AND id = ?`,
		release.Name,
		release.Environment,
		formatTime(release.ObservedAt),
		release.Version,
		release.CommitSHA,
		release.URL,
		release.Notes,
		release.CreatedBy,
		projectID,
		rowID,
	); err != nil {
		return Release{}, err
	}

	return s.GetRelease(ctx, releaseID)
}

func (s *Store) DeleteRelease(ctx context.Context, releaseID string) error {
	rowID, err := parseID(releaseIDPrefix, releaseID)
	if err != nil {
		return err
	}
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM releases WHERE project_id = ? AND id = ?`, projectID, rowID)
	return err
}

func scanRelease(scanner interface {
	Scan(dest ...any) error
}) (Release, error) {
	var (
		id        int64
		item      Release
		observed  string
		createdAt string
	)
	if err := scanner.Scan(
		&id,
		&item.Name,
		&item.Environment,
		&observed,
		&item.Version,
		&item.CommitSHA,
		&item.URL,
		&item.Notes,
		&item.CreatedBy,
		&createdAt,
	); err != nil {
		return Release{}, err
	}
	item.ID = formatID(releaseIDPrefix, id)
	item.ObservedAt, _ = parseTime(observed)
	item.CreatedAt, _ = parseTime(createdAt)
	return item, nil
}
