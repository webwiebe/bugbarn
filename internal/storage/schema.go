package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func (s *core) init(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return wrapErr(err, "init")
	}

	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("migration fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, sub,
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (slug, name) VALUES (?, ?) ON CONFLICT(slug) DO NOTHING`,
		defaultProject, "Default Project",
	); err != nil {
		return wrapErr(err, "init")
	}

	var projectID int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE slug = ?`, defaultProject,
	).Scan(&projectID); err != nil {
		return wrapErr(err, "init")
	}
	s.defaultProjectID = projectID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapErr(err, "init")
	}
	defer tx.Rollback()
	if err := migrateIssuePrefixes(ctx, tx); err != nil {
		return wrapErr(err, "init")
	}
	return tx.Commit()
}

// numberProjectIssues numbers the project's unnumbered issues 1..n in id order
// and stores the prefix and the counter n on the project.
func numberProjectIssues(ctx context.Context, tx *sql.Tx, projectID int64, prefix string) error {
	issueRows, err := tx.QueryContext(ctx,
		`SELECT id FROM issues WHERE project_id = ? AND issue_number = 0 ORDER BY id ASC`, projectID)
	if err != nil {
		return wrapErr(err, "numberProjectIssues")
	}
	var issueIDs []int64
	for issueRows.Next() {
		var id int64
		if err := issueRows.Scan(&id); err != nil {
			issueRows.Close()
			return wrapErr(err, "numberProjectIssues")
		}
		issueIDs = append(issueIDs, id)
	}
	issueRows.Close()

	for i, id := range issueIDs {
		if _, err := tx.ExecContext(ctx,
			`UPDATE issues SET issue_number = ? WHERE id = ?`, i+1, id); err != nil {
			return wrapErr(err, "numberProjectIssues")
		}
	}

	counter := len(issueIDs)
	_, err = tx.ExecContext(ctx,
		`UPDATE projects SET issue_prefix = ?, issue_counter = ? WHERE id = ?`,
		prefix, counter, projectID)
	return wrapErr(err, "numberProjectIssues")
}

// migrateIssuePrefixes assigns issue_prefix to projects that don't have one yet,
// and assigns sequential issue_number to issues that have issue_number=0.
func migrateIssuePrefixes(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, slug FROM projects WHERE issue_prefix = ''`)
	if err != nil {
		return wrapErr(err, "migrateIssuePrefixes")
	}
	var projects []struct {
		id   int64
		slug string
	}
	for rows.Next() {
		var p struct {
			id   int64
			slug string
		}
		if err := rows.Scan(&p.id, &p.slug); err != nil {
			rows.Close()
			return wrapErr(err, "migrateIssuePrefixes")
		}
		projects = append(projects, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return wrapErr(err, "migrateIssuePrefixes")
	}

	usedPrefixes := make(map[string]bool)
	existingRows, err := tx.QueryContext(ctx, `SELECT issue_prefix FROM projects WHERE issue_prefix != ''`)
	if err != nil {
		return wrapErr(err, "migrateIssuePrefixes")
	}
	for existingRows.Next() {
		var p string
		if err := existingRows.Scan(&p); err != nil {
			existingRows.Close()
			return wrapErr(err, "migrateIssuePrefixes")
		}
		usedPrefixes[p] = true
	}
	existingRows.Close()

	for _, p := range projects {
		prefix := deriveIssuePrefix(p.slug)
		base := prefix
		for i := 2; usedPrefixes[prefix]; i++ {
			prefix = fmt.Sprintf("%s%d", base, i)
		}
		usedPrefixes[prefix] = true

		if err := numberProjectIssues(ctx, tx, p.id, prefix); err != nil {
			return wrapErr(err, "migrateIssuePrefixes")
		}
	}
	return nil
}
