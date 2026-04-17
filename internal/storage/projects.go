package storage

import (
	"context"
	"time"
)

// CreateProject inserts a new project row; returns an error if the slug already exists.
func (s *Store) CreateProject(ctx context.Context, name, slug string) (Project, error) {
	now := formatTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO projects (name, slug, created_at) VALUES (?, ?, ?)`,
		name, slug, now,
	)
	if err != nil {
		return Project{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Project{}, err
	}
	return Project{ID: id, Name: name, Slug: slug, CreatedAt: time.Now().UTC()}, nil
}

// ListProjects returns all projects ordered by id.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, slug, created_at FROM projects ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &createdAt); err != nil {
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
	err := s.db.QueryRowContext(ctx, `SELECT id, name, slug, created_at FROM projects WHERE slug = ?`, slug).
		Scan(&p.ID, &p.Name, &p.Slug, &createdAt)
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
