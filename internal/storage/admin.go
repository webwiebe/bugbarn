package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	releaseIDPrefix = "release-"
	alertIDPrefix   = "alert-"
	sourceMapPrefix = "sourcemap-"
)

type SourceMap struct {
	ID          string
	Release     string
	Dist        string
	BundleURL   string
	Name        string
	ContentType string
	SizeBytes   int64
	UploadedAt  time.Time
}

type SourceMapUpload struct {
	Release     string
	Dist        string
	BundleURL   string
	Name        string
	ContentType string
	Blob        []byte
}

func (s *Store) ResolveIssue(ctx context.Context, issueID string) (Issue, error) {
	return s.setIssueStatus(ctx, issueID, "resolved")
}

func (s *Store) ReopenIssue(ctx context.Context, issueID string) (Issue, error) {
	return s.setIssueStatus(ctx, issueID, "unresolved")
}

func (s *Store) setIssueStatus(ctx context.Context, issueID, status string) (Issue, error) {
	rowID, err := parseID(issueIDPrefix, issueID)
	if err != nil {
		return Issue{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Issue{}, err
	}
	defer tx.Rollback()

	now := formatTime(time.Now().UTC())
	query := `
UPDATE issues
SET status = ?,
	resolved_at = CASE WHEN ? = 'resolved' THEN ? ELSE resolved_at END,
	reopened_at = CASE WHEN ? = 'unresolved' THEN ? ELSE reopened_at END,
	updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND project_id = ?`
	if _, err := tx.ExecContext(ctx, query, status, status, now, status, now, rowID, s.defaultProjectID); err != nil {
		return Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return Issue{}, err
	}

	return s.GetIssue(ctx, issueID)
}

func (s *Store) ListReleases(ctx context.Context) ([]Release, error) {
	rows, err := s.db.QueryContext(ctx, `
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
		s.defaultProjectID,
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
		return Release{}, err
	}

	row := s.db.QueryRowContext(ctx, `
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
		s.defaultProjectID,
		rowID,
	)
	return scanRelease(row)
}

func (s *Store) CreateRelease(ctx context.Context, release Release) (Release, error) {
	if strings.TrimSpace(release.Name) == "" {
		return Release{}, errors.New("release name is required")
	}
	if release.ObservedAt.IsZero() {
		release.ObservedAt = time.Now().UTC()
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
		s.defaultProjectID,
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
		return Release{}, err
	}
	if strings.TrimSpace(release.Name) == "" {
		return Release{}, errors.New("release name is required")
	}
	if release.ObservedAt.IsZero() {
		release.ObservedAt = time.Now().UTC()
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
		s.defaultProjectID,
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
	_, err = s.db.ExecContext(ctx, `DELETE FROM releases WHERE project_id = ? AND id = ?`, s.defaultProjectID, rowID)
	return err
}

func (s *Store) ListAlerts(ctx context.Context) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
	id,
	name,
	enabled,
	severity,
	rule_json,
	created_at,
	updated_at
FROM alerts
WHERE project_id = ?
ORDER BY id DESC`,
		s.defaultProjectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		item, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, item)
	}
	return alerts, rows.Err()
}

func (s *Store) GetAlert(ctx context.Context, alertID string) (Alert, error) {
	rowID, err := parseID(alertIDPrefix, alertID)
	if err != nil {
		return Alert{}, err
	}
	row := s.db.QueryRowContext(ctx, `
SELECT
	id,
	name,
	enabled,
	severity,
	rule_json,
	created_at,
	updated_at
FROM alerts
WHERE project_id = ? AND id = ?`,
		s.defaultProjectID,
		rowID,
	)
	return scanAlert(row)
}

func (s *Store) CreateAlert(ctx context.Context, alert Alert) (Alert, error) {
	if strings.TrimSpace(alert.Name) == "" {
		return Alert{}, errors.New("alert name is required")
	}
	if alert.Rule == nil {
		alert.Rule = map[string]any{}
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
INSERT INTO alerts (
	project_id,
	name,
	enabled,
	severity,
	rule_json
) VALUES (?, ?, ?, ?, ?)`,
		s.defaultProjectID,
		alert.Name,
		boolToInt(alert.Enabled),
		alert.Severity,
		mustMarshalObject(alert.Rule),
	)
	if err != nil {
		return Alert{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Alert{}, err
	}
	alert.ID = formatID(alertIDPrefix, id)
	alert.CreatedAt = now
	alert.UpdatedAt = now
	return alert, nil
}

func (s *Store) UpdateAlert(ctx context.Context, alertID string, alert Alert) (Alert, error) {
	rowID, err := parseID(alertIDPrefix, alertID)
	if err != nil {
		return Alert{}, err
	}
	if strings.TrimSpace(alert.Name) == "" {
		return Alert{}, errors.New("alert name is required")
	}
	if alert.Rule == nil {
		alert.Rule = map[string]any{}
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE alerts
SET name = ?, enabled = ?, severity = ?, rule_json = ?, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND id = ?`,
		alert.Name,
		boolToInt(alert.Enabled),
		alert.Severity,
		mustMarshalObject(alert.Rule),
		s.defaultProjectID,
		rowID,
	); err != nil {
		return Alert{}, err
	}
	return s.GetAlert(ctx, alertID)
}

func (s *Store) DeleteAlert(ctx context.Context, alertID string) error {
	rowID, err := parseID(alertIDPrefix, alertID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM alerts WHERE project_id = ? AND id = ?`, s.defaultProjectID, rowID)
	return err
}

func (s *Store) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT key, value
FROM settings
WHERE project_id = ?`,
		s.defaultProjectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Store) UpdateSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for key, value := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO settings (project_id, key, value, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(project_id, key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
			s.defaultProjectID,
			key,
			value,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) UploadSourceMap(ctx context.Context, upload SourceMapUpload) (SourceMap, error) {
	if strings.TrimSpace(upload.Release) == "" {
		return SourceMap{}, errors.New("source map release is required")
	}
	if strings.TrimSpace(upload.BundleURL) == "" {
		return SourceMap{}, errors.New("source map bundle URL is required")
	}
	now := time.Now().UTC()
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
		s.defaultProjectID,
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

func scanAlert(scanner interface {
	Scan(dest ...any) error
}) (Alert, error) {
	var (
		id        int64
		item      Alert
		ruleRaw   []byte
		createdAt string
		updatedAt string
		enabled   int
	)
	if err := scanner.Scan(
		&id,
		&item.Name,
		&enabled,
		&item.Severity,
		&ruleRaw,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Alert{}, err
	}
	item.ID = formatID(alertIDPrefix, id)
	item.Enabled = enabled != 0
	item.CreatedAt, _ = parseTime(createdAt)
	item.UpdatedAt, _ = parseTime(updatedAt)
	if len(ruleRaw) > 0 {
		if err := json.Unmarshal(ruleRaw, &item.Rule); err != nil {
			return Alert{}, err
		}
	}
	if item.Rule == nil {
		item.Rule = map[string]any{}
	}
	return item, nil
}

func mustMarshalObject(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}
