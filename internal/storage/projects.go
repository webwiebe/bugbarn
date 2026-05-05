package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
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
		return Project{}, wrapErr(err, "create project")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Project{}, err
	}
	return Project{ID: id, Name: name, Slug: slug, Status: "active", IssuePrefix: prefix, CreatedAt: time.Now().UTC()}, nil
}

// ListProjects returns all projects ordered alphabetically by name.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT id, name, slug, status, issue_prefix, issue_counter, group_id, created_at FROM projects ORDER BY name ASC`)
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

// ProjectBySlug returns the project with the given slug.
func (s *Store) ProjectBySlug(ctx context.Context, slug string) (Project, error) {
	var p Project
	var createdAt string
	err := s.readDB().QueryRowContext(ctx, `SELECT id, name, slug, status, issue_prefix, issue_counter, group_id, created_at FROM projects WHERE slug = ?`, slug).
		Scan(&p.ID, &p.Name, &p.Slug, &p.Status, &p.IssuePrefix, &p.IssueCounter, &p.GroupID, &createdAt)
	if err != nil {
		return Project{}, wrapNotFound(err, "project not found")
	}
	p.CreatedAt, _ = parseTime(createdAt)
	return p, nil
}

// EnsureProject returns the project with the given slug, creating it if it does not exist.
// If the slug matches a project alias, it returns the target project.
func (s *Store) EnsureProject(ctx context.Context, slug string) (Project, error) {
	p, err := s.ProjectBySlug(ctx, slug)
	if err == nil {
		return p, nil
	}
	// Check if slug is an alias.
	targetID, aliasErr := s.ResolveAlias(ctx, slug)
	if aliasErr == nil {
		return s.projectByID(ctx, targetID)
	}
	return s.CreateProject(ctx, slug, slug)
}

// EnsureProjectPending returns the project with the given slug, creating it
// with status=pending if it does not exist. If the slug matches a project alias,
// it returns the target project.
func (s *Store) EnsureProjectPending(ctx context.Context, slug string) (Project, error) {
	p, err := s.ProjectBySlug(ctx, slug)
	if err == nil {
		return p, nil
	}
	// Check if slug is an alias.
	targetID, aliasErr := s.ResolveAlias(ctx, slug)
	if aliasErr == nil {
		return s.projectByID(ctx, targetID)
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

// DeleteProject removes the project with the given slug. Related rows are
// removed via ON DELETE CASCADE foreign keys in the schema.
func (s *Store) DeleteProject(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE slug=?`, slug)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFound("project not found", nil)
	}
	return nil
}

// ApproveProject sets status='active' for the project with the given slug.
func (s *Store) ApproveProject(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET status='active' WHERE slug=?`, slug)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFound("project not found", nil)
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
		return 0, wrapNotFound(err, "issue not found")
	}
	return rowID, nil
}

// projectByID returns a project by its numeric ID.
func (s *Store) projectByID(ctx context.Context, id int64) (Project, error) {
	var p Project
	var createdAt string
	err := s.readDB().QueryRowContext(ctx, `SELECT id, name, slug, status, issue_prefix, issue_counter, group_id, created_at FROM projects WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Slug, &p.Status, &p.IssuePrefix, &p.IssueCounter, &p.GroupID, &createdAt)
	if err != nil {
		return Project{}, wrapNotFound(err, "project not found")
	}
	p.CreatedAt, _ = parseTime(createdAt)
	return p, nil
}

// --- Alias operations ---

// CreateAlias creates a slug alias pointing to the given project ID.
func (s *Store) CreateAlias(ctx context.Context, aliasSlug string, projectID int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO project_aliases (alias_slug, project_id) VALUES (?, ?)`, aliasSlug, projectID)
	if err != nil {
		return wrapErr(err, "create alias")
	}
	return nil
}

// DeleteAlias removes a slug alias.
func (s *Store) DeleteAlias(ctx context.Context, aliasSlug string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM project_aliases WHERE alias_slug = ?`, aliasSlug)
	if err != nil {
		return wrapErr(err, "delete alias")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFound("alias not found", nil)
	}
	return nil
}

// ResolveAlias returns the target project_id for an alias slug.
func (s *Store) ResolveAlias(ctx context.Context, slug string) (int64, error) {
	var projectID int64
	err := s.readDB().QueryRowContext(ctx, `SELECT project_id FROM project_aliases WHERE alias_slug = ?`, slug).Scan(&projectID)
	if err != nil {
		return 0, wrapNotFound(err, "alias not found")
	}
	return projectID, nil
}

// --- Rename and Merge ---

// RenameProject updates the project's slug and name, creating an alias from the old slug.
func (s *Store) RenameProject(ctx context.Context, oldSlug, newSlug, newName string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var projectID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, oldSlug).Scan(&projectID)
	if err != nil {
		return wrapNotFound(err, "project not found")
	}

	_, err = tx.ExecContext(ctx, `UPDATE projects SET slug = ?, name = ? WHERE id = ?`, newSlug, newName, projectID)
	if err != nil {
		return wrapErr(err, "rename project")
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO project_aliases (alias_slug, project_id) VALUES (?, ?)`, oldSlug, projectID)
	if err != nil {
		return wrapErr(err, "create rename alias")
	}

	return tx.Commit()
}

// MergeProjects moves all data from the source project to the target project,
// creates an alias from the source slug, and deletes the source project.
func (s *Store) MergeProjects(ctx context.Context, sourceSlug, targetSlug string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sourceID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, sourceSlug).Scan(&sourceID)
	if err != nil {
		return apperr.NotFound("source project not found", err)
	}

	var targetID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, targetSlug).Scan(&targetID)
	if err != nil {
		return apperr.NotFound("target project not found", err)
	}

	// Move issues (events reference issue_id, so they follow automatically via FK).
	if _, err := tx.ExecContext(ctx, `UPDATE issues SET project_id = ? WHERE project_id = ?`, targetID, sourceID); err != nil {
		return apperr.Internal("merge: move issues", err)
	}

	// Move events that directly reference project_id.
	if _, err := tx.ExecContext(ctx, `UPDATE events SET project_id = ? WHERE project_id = ?`, targetID, sourceID); err != nil {
		return apperr.Internal("merge: move events", err)
	}

	// Move event facets.
	if _, err := tx.ExecContext(ctx, `UPDATE event_facets SET project_id = ? WHERE project_id = ?`, targetID, sourceID); err != nil {
		return apperr.Internal("merge: move event_facets", err)
	}

	// Move analytics pageviews.
	if _, err := tx.ExecContext(ctx, `UPDATE analytics_pageviews SET project_id = ? WHERE project_id = ?`, targetID, sourceID); err != nil {
		return apperr.Internal("merge: move analytics", err)
	}

	// Move alerts.
	if _, err := tx.ExecContext(ctx, `UPDATE alerts SET project_id = ? WHERE project_id = ?`, targetID, sourceID); err != nil {
		return apperr.Internal("merge: move alerts", err)
	}

	// Create alias from source slug → target project.
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_aliases (alias_slug, project_id) VALUES (?, ?)`, sourceSlug, targetID); err != nil {
		return wrapErr(err, "merge: create alias")
	}

	// Delete source project.
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, sourceID); err != nil {
		return apperr.Internal("merge: delete source", err)
	}

	return tx.Commit()
}
