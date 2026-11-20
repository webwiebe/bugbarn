package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// shiftSourceIssueNumbers moves the source project's issue numbers above the
// target's counter and raises the target's counter by the source's. A merged
// issue keeps its row but takes the target's prefix, so without the shift
// "GEO-459" would name two issues and every lookup by display id would pick
// one of them.
func shiftSourceIssueNumbers(ctx context.Context, tx *sql.Tx, sourceID, targetID int64) error {
	var targetCounter, sourceCounter int64
	if err := tx.QueryRowContext(ctx, `SELECT issue_counter FROM projects WHERE id = ?`, targetID).Scan(&targetCounter); err != nil {
		return apperr.Internal("merge: read target counter", err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(issue_number), 0) FROM issues WHERE project_id = ?`, sourceID).Scan(&sourceCounter); err != nil {
		return apperr.Internal("merge: read source numbers", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE issues SET issue_number = issue_number + ? WHERE project_id = ?`, targetCounter, sourceID); err != nil {
		return apperr.Internal("merge: shift issue numbers", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET issue_counter = issue_counter + ? WHERE id = ?`, sourceCounter, targetID); err != nil {
		return apperr.Internal("merge: raise issue counter", err)
	}
	return nil
}

// RenumberDuplicateIssues gives a new number to the issues of a project that
// share an issue number with another issue, so each display id names one issue
// again. Only issues that carry the given environment are renumbered: they are
// the ones a merge moved in. When every issue of a group carries it, the one
// with the lowest row id keeps the number. The new numbers continue after the
// project's counter. It returns the old and new display numbers.
func (s *ProjectStore) RenumberDuplicateIssues(ctx context.Context, slug, environment string) ([][2]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrapErr(err, "renumber issues")
	}
	defer tx.Rollback()
	var projectID, counter int64
	if err := tx.QueryRowContext(ctx, `SELECT id, issue_counter FROM projects WHERE slug = ?`, slug).Scan(&projectID, &counter); err != nil {
		return nil, wrapNotFound(err, "project not found")
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id, i.issue_number,
			EXISTS (SELECT 1 FROM issue_facets f WHERE f.issue_id = i.id
				AND f.facet_key IN ('environment', 'attributes.environment') AND f.facet_value = ?)
		FROM issues i
		WHERE i.project_id = ? AND i.issue_number IN (
			SELECT issue_number FROM issues WHERE project_id = ? GROUP BY issue_number HAVING COUNT(*) > 1)
		ORDER BY i.issue_number, i.id`, environment, projectID, projectID)
	if err != nil {
		return nil, apperr.Internal("renumber: find duplicates", err)
	}
	type member struct {
		id, number int64
		matches    bool
	}
	groups := map[int64][]member{}
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.id, &m.number, &m.matches); err != nil {
			_ = rows.Close()
			return nil, apperr.Internal("renumber: scan", err)
		}
		groups[m.number] = append(groups[m.number], m)
	}
	if err := rows.Close(); err != nil {
		return nil, apperr.Internal("renumber: find duplicates", err)
	}
	numbers := make([]int64, 0, len(groups))
	for n := range groups {
		numbers = append(numbers, n)
	}
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })

	var changed [][2]int64
	for _, n := range numbers {
		members := groups[n]
		all := true
		for _, m := range members {
			all = all && m.matches
		}
		for i, m := range members {
			if !m.matches || (all && i == 0) {
				continue
			}
			counter++
			if _, err := tx.ExecContext(ctx, `UPDATE issues SET issue_number = ? WHERE id = ?`, counter, m.id); err != nil {
				return nil, apperr.Internal(fmt.Sprintf("renumber issue %d", m.id), err)
			}
			changed = append(changed, [2]int64{n, counter})
		}
	}
	if len(changed) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET issue_counter = ? WHERE id = ?`, counter, projectID); err != nil {
			return nil, apperr.Internal("renumber: raise counter", err)
		}
	}
	return changed, wrapErr(tx.Commit(), "renumber issues")
}
