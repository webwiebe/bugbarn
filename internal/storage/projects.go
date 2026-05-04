package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateProject inserts a new project row; returns an error if the slug already exists.
func (s *Store) CreateProject(ctx context.Context, name, slug string) (Project, error) {
	prefix, err := s.uniqueIssuePrefix(ctx, slug)
	if err != nil {
		return Project{}, err
	}
	now := formatTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO projects (name, slug, status, issue_prefix, issue_counter, created_at) VALUES (?, ?, 'active', ?, 0, ?)`,
		name, slug, prefix, now,
	)
	if err != nil {
		return Project{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Project{}, err
	}
	return Project{ID: id, Name: name, Slug: slug, Status: "active", IssuePrefix: prefix, CreatedAt: time.Now().UTC()}, nil
}

// ListProjects returns all projects ordered by id.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT id, name, slug, status, issue_prefix, issue_counter, created_at FROM projects ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Status, &p.IssuePrefix, &p.IssueCounter, &createdAt); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = parseTime(createdAt)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// ProjectBySlug returns the project with the given slug.
func (s *Store) ProjectBySlug(ctx context.Context, slug string) (Project, error) {
	var p Project
	var createdAt string
	err := s.readDB().QueryRowContext(ctx, `SELECT id, name, slug, status, issue_prefix, issue_counter, created_at FROM projects WHERE slug = ?`, slug).
		Scan(&p.ID, &p.Name, &p.Slug, &p.Status, &p.IssuePrefix, &p.IssueCounter, &createdAt)
	if err != nil {
		return Project{}, err
	}
	p.CreatedAt, _ = parseTime(createdAt)
	return p, nil
}

// EnsureProject returns the project with the given slug, creating it if it does not exist.
func (s *Store) EnsureProject(ctx context.Context, slug string) (Project, error) {
	p, err := s.ProjectBySlug(ctx, slug)
	if err == nil {
		return p, nil
	}
	return s.CreateProject(ctx, slug, slug)
}

// EnsureProjectPending returns the project with the given slug, creating it
// with status=pending if it does not exist.
func (s *Store) EnsureProjectPending(ctx context.Context, slug string) (Project, error) {
	p, err := s.ProjectBySlug(ctx, slug)
	if err == nil {
		return p, nil
	}
	return s.CreateProjectPending(ctx, slug)
}

// CreateProjectPending inserts a new project with status=pending.
func (s *Store) CreateProjectPending(ctx context.Context, slug string) (Project, error) {
	prefix, err := s.uniqueIssuePrefix(ctx, slug)
	if err != nil {
		return Project{}, err
	}
	now := formatTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO projects (name, slug, status, issue_prefix, issue_counter, created_at) VALUES (?, ?, 'pending', ?, 0, ?)`,
		slug, slug, prefix, now,
	)
	if err != nil {
		return Project{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Project{}, err
	}
	return Project{ID: id, Name: slug, Slug: slug, Status: "pending", IssuePrefix: prefix, CreatedAt: time.Now().UTC()}, nil
}

// ApproveProject sets status='active' for the project with the given slug.
func (s *Store) ApproveProject(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET status='active' WHERE slug=?`, slug)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// uniqueIssuePrefix derives a unique issue prefix from the slug, appending a
// numeric suffix if the derived prefix is already taken.
func (s *Store) uniqueIssuePrefix(ctx context.Context, slug string) (string, error) {
	prefix := deriveIssuePrefix(slug)
	base := prefix
	for i := 2; ; i++ {
		var count int
		err := s.readDB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM projects WHERE issue_prefix = ?`, prefix).Scan(&count)
		if err != nil {
			return "", err
		}
		if count == 0 {
			return prefix, nil
		}
		prefix = fmt.Sprintf("%s%d", base, i)
	}
}

// IssueRowIDByDisplayID resolves a Jira-style issue ID (e.g. "BW-42") to the
// database row ID. Falls back to legacy "issue-NNNNNN" format.
func (s *Store) IssueRowIDByDisplayID(ctx context.Context, displayID string) (int64, error) {
	prefix, number, err := parseIssueID(displayID)
	if err != nil {
		return 0, err
	}
	if prefix == "" {
		// Legacy format: number is the row ID.
		return int64(number), nil
	}
	var rowID int64
	err = s.readDB().QueryRowContext(ctx, `
SELECT i.id FROM issues i
JOIN projects p ON p.id = i.project_id
WHERE p.issue_prefix = ? AND i.issue_number = ?`, prefix, number).Scan(&rowID)
	if err != nil {
		return 0, err
	}
	return rowID, nil
}
