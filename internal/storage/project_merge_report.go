package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// MergeProjectsReport merges like MergeProjectsAs and returns what it did. With
// dryRun it runs every step inside the transaction, so the counts include the
// effect of folding and stamping, and rolls back. A source slug that is already
// an alias of the target returns an AlreadyMerged report with zero counts.
func (s *ProjectStore) MergeProjectsReport(ctx context.Context, sourceSlug, targetSlug, environment string,
	dryRun bool) (domain.MergeReport, error) {
	rep := domain.MergeReport{Source: sourceSlug, Target: targetSlug, Environment: environment, DryRun: dryRun, Moved: map[string]int64{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rep, wrapErr(err, "merge projects")
	}
	defer tx.Rollback()

	sourceID, targetID, err := mergeProjectIDs(ctx, tx, sourceSlug, targetSlug)
	if errors.Is(err, errAlreadyMerged) {
		rep.AlreadyMerged = true
		for _, t := range projectTables {
			rep.Moved[t.name] = 0
		}
		return rep, nil
	}
	if err != nil {
		return rep, err
	}
	if err := applyMerge(ctx, tx, sourceID, targetID, sourceSlug, environment, &rep); err != nil {
		return rep, err
	}
	if dryRun {
		return rep, nil
	}
	return rep, wrapErr(tx.Commit(), "merge projects")
}

var errAlreadyMerged = errors.New("source already merged into the target")

// mergeProjectIDs resolves both slugs. A missing source that is already an
// alias of the target returns errAlreadyMerged.
func mergeProjectIDs(ctx context.Context, tx *sql.Tx, sourceSlug, targetSlug string) (sourceID, targetID int64, err error) {
	err = tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, sourceSlug).Scan(&sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		merged, aerr := aliasPointsAt(ctx, tx, sourceSlug, targetSlug)
		if aerr != nil {
			return 0, 0, aerr
		}
		if merged {
			return 0, 0, errAlreadyMerged
		}
	}
	if err != nil {
		return 0, 0, wrapNotFound(err, "source project not found")
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, targetSlug).Scan(&targetID); err != nil {
		return 0, 0, wrapNotFound(err, "target project not found")
	}
	return sourceID, targetID, nil
}

// applyMerge counts, then runs every merge step inside tx. The first step
// that fails ends it.
func applyMerge(ctx context.Context, tx *sql.Tx, sourceID, targetID int64, sourceSlug, environment string,
	rep *domain.MergeReport) error {
	exec := func(what, query string, args ...any) func() error {
		return func() error {
			_, err := tx.ExecContext(ctx, query, args...)
			return wrapErr(err, what)
		}
	}
	steps := []func() error{
		func() error { return countMerge(ctx, tx, sourceID, targetID, rep) },
		func() error {
			if environment == "" {
				return nil
			}
			return stampEnvironment(ctx, tx, sourceID, environment)
		},
		func() error { return foldCollidingIssues(ctx, tx, sourceID, targetID) },
		func() error { return shiftSourceIssueNumbers(ctx, tx, sourceID, targetID) },
		func() error { return wrapErr(moveProjectRows(ctx, tx, sourceID, targetID), "MergeProjects") },
		exec("merge: create alias", `INSERT INTO project_aliases (alias_slug, project_id) VALUES (?, ?)`, sourceSlug, targetID),
		exec("merge: delete source", `DELETE FROM projects WHERE id = ?`, sourceID),
		func() error {
			err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE project_id = ?`, targetID).Scan(&rep.TargetIssuesAfter)
			return wrapErr(err, "merge: count target issues")
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func aliasPointsAt(ctx context.Context, tx *sql.Tx, alias, targetSlug string) (bool, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM project_aliases a JOIN projects p ON p.id = a.project_id
		WHERE a.alias_slug = ? AND p.slug = ?`, alias, targetSlug).Scan(&n)
	if err != nil {
		return false, apperr.Internal("merge: read alias", err)
	}
	return n > 0, nil
}

// countMerge fills the counts that must be read before the merge changes rows.
func countMerge(ctx context.Context, tx *sql.Tx, sourceID, targetID int64, rep *domain.MergeReport) error {
	scan := func(what, query string, dest *int64, args ...any) error {
		return wrapErr(tx.QueryRowContext(ctx, query, args...).Scan(dest), "merge: count "+what)
	}
	for _, t := range projectTables {
		var n int64
		if err := scan(t.name, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE project_id = ?`, t.name), &n, sourceID); err != nil {
			return err
		}
		rep.Moved[t.name] = n
	}
	if err := scan("folded issues", `
		SELECT COUNT(*) FROM issues s WHERE s.project_id = ?
		AND EXISTS (SELECT 1 FROM issues t WHERE t.project_id = ? AND t.fingerprint = s.fingerprint)`,
		&rep.Folded, sourceID, targetID); err != nil {
		return err
	}
	return scan("target counter", `
		SELECT MAX(issue_counter, COALESCE((SELECT MAX(issue_number) FROM issues WHERE project_id = projects.id), 0))
		FROM projects WHERE id = ?`, &rep.IssueNumberShift, targetID)
}
