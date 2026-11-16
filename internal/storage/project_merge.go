package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// MergeProjects moves everything the source project owns into the target and
// archives the source (bugbarn#282). Nothing is deleted:
//
//   - Issues with a fingerprint the target already has fold into that issue (their
//     events, regressions and facets follow, the counts add up); the others move
//     and take new issue numbers after the target's, so numbers stay unique.
//   - Each moved issue carries an `environment` facet: its own, or the merge's
//     environment when it had none.
//   - The source row stays, status archived, pointing at the target with the
//     environment. Its ingest keys and slug keep working and resolve there.
//   - The merge, with its counts, is recorded in project_merges.
//
// environment must be a valid environment name; callers default it.
func (s *ProjectStore) MergeProjects(ctx context.Context, sourceSlug, targetSlug, environment string) (domain.MergeResult, error) {
	env, ok := domain.NormalizeEnvironment(environment)
	if !ok {
		return domain.MergeResult{}, apperr.InvalidInput("environment must be a lowercase name such as production, staging or testing", nil)
	}
	if sourceSlug == targetSlug {
		return domain.MergeResult{}, apperr.InvalidInput("source and target are the same project", nil)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MergeResult{}, wrapErr(err, "merge projects")
	}
	defer tx.Rollback()

	src, err := mergeParty(ctx, tx, sourceSlug, "source")
	if err != nil {
		return domain.MergeResult{}, err
	}
	dst, err := mergeParty(ctx, tx, targetSlug, "target")
	if err != nil {
		return domain.MergeResult{}, err
	}

	res := domain.MergeResult{Source: src.slug, Target: dst.slug, Environment: env, IssueNumberOffset: dst.counter}
	if res.Counts, err = countMergeRows(ctx, tx, src.id); err != nil {
		return domain.MergeResult{}, err
	}
	if err := stampEnvironment(ctx, tx, src.id, env); err != nil {
		return domain.MergeResult{}, err
	}
	folded, err := foldCollidingIssues(ctx, tx, src.id, dst.id)
	if err != nil {
		return domain.MergeResult{}, err
	}
	res.Counts.IssuesFolded = folded
	res.Counts.IssuesMoved -= folded

	if err := renumberAndArchive(ctx, tx, src, dst, env); err != nil {
		return domain.MergeResult{}, err
	}

	res.MergedAt = time.Now().UTC()
	counts, _ := json.Marshal(res.Counts)
	r, err := tx.ExecContext(ctx, `
INSERT INTO project_merges (source_id, source_slug, target_id, target_slug, environment, issue_number_offset, counts_json, merged_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		src.id, src.slug, dst.id, dst.slug, env, dst.counter, string(counts), formatTime(res.MergedAt))
	if err != nil {
		return domain.MergeResult{}, apperr.Internal("merge: record", err)
	}
	res.ID, _ = r.LastInsertId()
	return res, wrapErr(tx.Commit(), "merge projects")
}

// renumberAndArchive gives the issues that move numbers after the target's own,
// so prefix+number stays unique inside the target, moves the rest of the
// source's rows and archives the source row.
func renumberAndArchive(ctx context.Context, tx *sql.Tx, src, dst mergeSide, env string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE issues SET issue_number = issue_number + ? WHERE project_id = ?`, dst.counter, src.id); err != nil {
		return apperr.Internal("merge: renumber issues", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET issue_counter = issue_counter + ? WHERE id = ?`, src.counter, dst.id); err != nil {
		return apperr.Internal("merge: advance issue counter", err)
	}
	if err := moveProjectRows(ctx, tx, src.id, dst.id); err != nil {
		return wrapErr(err, "MergeProjects")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET status = 'archived', merged_into = ?, merged_environment = ?, issue_counter = 0 WHERE id = ?`,
		dst.id, env, src.id); err != nil {
		return apperr.Internal("merge: archive source", err)
	}
	return nil
}

type mergeSide struct {
	id      int64
	slug    string
	counter int
}

// mergeParty loads one side of a merge and refuses a project that is already
// archived or pending.
func mergeParty(ctx context.Context, tx *sql.Tx, slug, role string) (mergeSide, error) {
	var m mergeSide
	var status string
	err := tx.QueryRowContext(ctx,
		`SELECT id, slug, status, issue_counter FROM projects WHERE slug = ?`, slug).Scan(&m.id, &m.slug, &status, &m.counter)
	if err != nil {
		return mergeSide{}, wrapNotFound(err, role+" project not found")
	}
	if status == domain.ProjectStatusArchived {
		return mergeSide{}, apperr.Conflict(role+" project is already merged into another project", nil)
	}
	return m, nil
}

// countMergeRows counts what the source owns before anything moves.
func countMergeRows(ctx context.Context, tx *sql.Tx, sourceID int64) (domain.MergeCounts, error) {
	var c domain.MergeCounts
	for _, q := range []struct {
		table string
		into  *int
	}{
		{"issues", &c.IssuesMoved}, {"events", &c.Events}, {"log_entries", &c.Logs},
		{"releases", &c.Releases}, {"alerts", &c.Alerts}, {"source_maps", &c.SourceMaps},
		{"analytics_pageviews", &c.AnalyticsPageviews}, {"api_keys", &c.APIKeys}, {"project_aliases", &c.Aliases},
	} {
		if err := tx.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE project_id = ?`, q.table), sourceID).Scan(q.into); err != nil {
			return domain.MergeCounts{}, apperr.Internal("merge: count "+q.table, err)
		}
	}
	return c, nil
}

// stampEnvironment gives every source issue without an environment facet the
// merge's environment, before the issues move or fold.
func stampEnvironment(ctx context.Context, tx *sql.Tx, sourceID int64, env string) error {
	_, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO issue_facets (project_id, facet_key, facet_value, issue_id)
SELECT i.project_id, 'environment', ?, i.id FROM issues i
WHERE i.project_id = ?
  AND NOT EXISTS (SELECT 1 FROM issue_facets f WHERE f.issue_id = i.id AND f.facet_key = 'environment')`,
		env, sourceID)
	if err != nil {
		return apperr.Internal("merge: stamp environment", err)
	}
	return nil
}

// foldCollidingIssues merges each source issue whose fingerprint the target
// already has into that target issue, and returns how many it folded.
func foldCollidingIssues(ctx context.Context, tx *sql.Tx, sourceID, targetID int64) (int, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT s.id, t.id FROM issues s JOIN issues t ON t.project_id = ? AND t.fingerprint = s.fingerprint
WHERE s.project_id = ?`, targetID, sourceID)
	if err != nil {
		return 0, apperr.Internal("merge: find colliding issues", err)
	}
	var pairs [][2]int64
	for rows.Next() {
		var p [2]int64
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return 0, apperr.Internal("merge: scan colliding issues", err)
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, apperr.Internal("merge: find colliding issues", err)
	}
	for _, p := range pairs {
		if err := foldIssue(ctx, tx, p[0], p[1], targetID); err != nil {
			return 0, err
		}
	}
	return len(pairs), nil
}

func foldIssue(ctx context.Context, tx *sql.Tx, from, into, targetProject int64) error {
	steps := []struct {
		what  string
		query string
		args  []any
	}{
		{"events", `UPDATE events SET issue_id = ?, project_id = ? WHERE issue_id = ?`, []any{into, targetProject, from}},
		{"regressions", `UPDATE regression_events SET issue_id = ?, project_id = ? WHERE issue_id = ?`, []any{into, targetProject, from}},
		{"facets", `INSERT OR IGNORE INTO issue_facets (project_id, facet_key, facet_value, issue_id)
SELECT ?, facet_key, facet_value, ? FROM issue_facets WHERE issue_id = ?`, []any{targetProject, into, from}},
		{"facet cleanup", `DELETE FROM issue_facets WHERE issue_id = ?`, []any{from}},
		// The folded issue is unresolved when either side still is, so a fix in
		// one environment does not hide a live failure in another.
		{"totals", `
UPDATE issues SET
  event_count = event_count + (SELECT event_count FROM issues WHERE id = ?),
  first_seen = MIN(first_seen, (SELECT first_seen FROM issues WHERE id = ?)),
  last_seen = MAX(last_seen, (SELECT last_seen FROM issues WHERE id = ?)),
  status = CASE WHEN status = 'unresolved' OR (SELECT status FROM issues WHERE id = ?) = 'unresolved'
                THEN 'unresolved' ELSE status END,
  resolved_at = CASE WHEN status = 'unresolved' OR (SELECT status FROM issues WHERE id = ?) = 'unresolved'
                     THEN '' ELSE resolved_at END
WHERE id = ?`, []any{from, from, from, from, from, into}},
		{"issue", `DELETE FROM issues WHERE id = ?`, []any{from}},
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, st.query, st.args...); err != nil {
			return apperr.Internal("merge: fold issue "+st.what, err)
		}
	}
	return nil
}

// ProjectMerges returns the recorded merges, newest first.
func (s *ProjectStore) ProjectMerges(ctx context.Context) ([]domain.MergeResult, error) {
	rows, err := s.readDB().QueryContext(ctx, `
SELECT id, source_slug, target_slug, environment, issue_number_offset, counts_json, merged_at
FROM project_merges ORDER BY id DESC`)
	if err != nil {
		return nil, wrapErr(err, "list project merges")
	}
	defer rows.Close()
	var out []domain.MergeResult
	for rows.Next() {
		var m domain.MergeResult
		var counts, at string
		if err := rows.Scan(&m.ID, &m.Source, &m.Target, &m.Environment, &m.IssueNumberOffset, &counts, &at); err != nil {
			return nil, wrapErr(err, "list project merges")
		}
		_ = json.Unmarshal([]byte(counts), &m.Counts)
		m.MergedAt, _ = parseTime(at)
		out = append(out, m)
	}
	return out, wrapErr(rows.Err(), "list project merges")
}
