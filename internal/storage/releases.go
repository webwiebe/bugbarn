package storage

import (
	"context"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

const releaseIDPrefix = "release-"

// ListReleases returns every release of the scoped project, newest first.
func (s *ReleaseStore) ListReleases(ctx context.Context) ([]Release, error) {
	return s.listReleases(ctx, -1)
}

// ListRecentReleases returns at most limit releases of the scoped project,
// newest first.
func (s *ReleaseStore) ListRecentReleases(ctx context.Context, limit int) ([]Release, error) {
	if limit <= 0 {
		return nil, apperr.InvalidInput("release list limit must be positive", nil)
	}
	return s.listReleases(ctx, limit)
}

// listReleases lists the scoped project's releases; a negative limit lists all.
func (s *ReleaseStore) listReleases(ctx context.Context, limit int) ([]Release, error) {
	projectID, ok := domain.ProjectIDFromContext(ctx)
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
ORDER BY observed_at DESC, id DESC
LIMIT ?`,
		projectID, limit,
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

func (s *ReleaseStore) GetRelease(ctx context.Context, releaseID string) (Release, error) {
	rowID, err := parseID(releaseIDPrefix, releaseID)
	if err != nil {
		return Release{}, apperr.InvalidInput("invalid release ID", err)
	}

	projectID, ok := domain.ProjectIDFromContext(ctx)
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

// maxReleaseNotesRunes bounds what a marker stores in notes. Deploy pipelines send
// a short line of under 40 characters; this leaves room for a short changelog.
const maxReleaseNotesRunes = 2000

// capReleaseNotes cuts notes to maxReleaseNotesRunes on a rune boundary.
func capReleaseNotes(notes string) string {
	if len(notes) <= maxReleaseNotesRunes {
		return notes
	}
	count := 0
	for i := range notes {
		if count == maxReleaseNotesRunes {
			return notes[:i]
		}
		count++
	}
	return notes
}

func (s *ReleaseStore) CreateRelease(ctx context.Context, release Release) (Release, error) {
	if strings.TrimSpace(release.Name) == "" {
		return Release{}, apperr.InvalidInput("release name is required", nil)
	}
	if release.ObservedAt.IsZero() {
		release.ObservedAt = time.Now().UTC()
	}
	release.Notes = capReleaseNotes(release.Notes)

	projectID, ok := domain.ProjectIDFromContext(ctx)
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

func (s *ReleaseStore) UpdateRelease(ctx context.Context, releaseID string, release Release) (Release, error) {
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
	release.Notes = capReleaseNotes(release.Notes)

	projectID, ok := domain.ProjectIDFromContext(ctx)
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

func (s *ReleaseStore) DeleteRelease(ctx context.Context, releaseID string) error {
	rowID, err := parseID(releaseIDPrefix, releaseID)
	if err != nil {
		return err
	}
	projectID, ok := domain.ProjectIDFromContext(ctx)
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
