package storage

import (
	"context"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// CreateGroup inserts a new project group.
func (s *Store) CreateGroup(ctx context.Context, name, slug string) (ProjectGroup, error) {
	now := formatTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `INSERT INTO project_groups (name, slug, created_at) VALUES (?, ?, ?)`, name, slug, now)
	if err != nil {
		return ProjectGroup{}, wrapErr(err, "create group")
	}
	id, _ := res.LastInsertId()
	return ProjectGroup{ID: id, Name: name, Slug: slug, CreatedAt: time.Now().UTC()}, nil
}

// ListGroups returns all project groups ordered by name.
func (s *Store) ListGroups(ctx context.Context) ([]ProjectGroup, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT id, name, slug, created_at FROM project_groups ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []ProjectGroup
	for rows.Next() {
		var g ProjectGroup
		var createdAt string
		if err := rows.Scan(&g.ID, &g.Name, &g.Slug, &createdAt); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = parseTime(createdAt)
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// DeleteGroup removes a project group by slug.
func (s *Store) DeleteGroup(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM project_groups WHERE slug = ?`, slug)
	if err != nil {
		return wrapErr(err, "delete group")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFound("group not found", nil)
	}
	return nil
}

// AssignProjectToGroup sets the group_id on a project.
func (s *Store) AssignProjectToGroup(ctx context.Context, projectSlug, groupSlug string) error {
	var groupID int64
	err := s.readDB().QueryRowContext(ctx, `SELECT id FROM project_groups WHERE slug = ?`, groupSlug).Scan(&groupID)
	if err != nil {
		return apperr.NotFound("group not found", err)
	}

	res, err := s.db.ExecContext(ctx, `UPDATE projects SET group_id = ? WHERE slug = ?`, groupID, projectSlug)
	if err != nil {
		return wrapErr(err, "assign project to group")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFound("project not found", nil)
	}
	return nil
}

// RemoveProjectFromGroup clears the group_id on a project.
func (s *Store) RemoveProjectFromGroup(ctx context.Context, projectSlug string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET group_id = NULL WHERE slug = ?`, projectSlug)
	if err != nil {
		return wrapErr(err, "remove project from group")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFound("project not found", nil)
	}
	return nil
}

// ListGroupProjects returns all projects belonging to a group.
func (s *Store) ListGroupProjects(ctx context.Context, groupSlug string) ([]Project, error) {
	var groupID int64
	err := s.readDB().QueryRowContext(ctx, `SELECT id FROM project_groups WHERE slug = ?`, groupSlug).Scan(&groupID)
	if err != nil {
		return nil, apperr.NotFound("group not found", err)
	}

	rows, err := s.readDB().QueryContext(ctx, `SELECT id, name, slug, status, issue_prefix, issue_counter, group_id, created_at FROM projects WHERE group_id = ? ORDER BY name ASC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Status, &p.IssuePrefix, &p.IssueCounter, &p.GroupID, &createdAt); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = parseTime(createdAt)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}
