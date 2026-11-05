package storage

import (
	"context"
	"database/sql"
	"errors"
)

// projectsOverReleaseCapQuery lists the projects with more than keep release
// rows. It is answered from idx_releases_project_observed_at alone (a covering
// index scan), so the hourly check stays cheap and runs on the read pool.
const projectsOverReleaseCapQuery = `
SELECT project_id FROM releases
GROUP BY project_id
HAVING COUNT(*) > ?`

// PruneReleases deletes at most limit release rows that fall outside the newest
// keep rows of their project, and reports how many it deleted.
//
// Newest means last inserted: observed_at comes from the client, so a marker
// sent with an old or a future time cannot push other rows out of the kept set
// or keep itself in it. No other table refers to a release row by id; issues
// and events match a release by its version string.
//
// The lookups run on the read pool. The write connection, which ingest shares,
// only runs the bounded deletes, and only for projects over the cap.
func (s *ReleaseStore) PruneReleases(ctx context.Context, keep, limit int) (int64, error) {
	if keep < 0 || limit <= 0 {
		return 0, nil
	}
	projects, err := s.projectsOverReleaseCap(ctx, keep)
	if err != nil {
		return 0, err
	}
	var deleted int64
	for _, projectID := range projects {
		if deleted >= int64(limit) {
			break
		}
		n, err := s.pruneProjectReleases(ctx, projectID, keep, limit-int(deleted))
		deleted += n
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}

func (s *ReleaseStore) projectsOverReleaseCap(ctx context.Context, keep int) ([]int64, error) {
	rows, err := s.readDB().QueryContext(ctx, projectsOverReleaseCapQuery, keep)
	if err != nil {
		return nil, wrapErr(err, "list projects over the release cap")
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, wrapErr(err, "list projects over the release cap")
		}
		ids = append(ids, id)
	}
	return ids, wrapErr(rows.Err(), "list projects over the release cap")
}

// pruneProjectReleases deletes at most limit of one project's rows older than
// its newest keep. The newest row outside the kept set is read first; every row
// at or below its id goes, and rows inserted meanwhile have higher ids.
func (s *ReleaseStore) pruneProjectReleases(ctx context.Context, projectID int64, keep, limit int) (int64, error) {
	var cutoff int64
	err := s.readDB().QueryRowContext(ctx, `
SELECT id FROM releases WHERE project_id = ?
ORDER BY id DESC
LIMIT 1 OFFSET ?`, projectID, keep).Scan(&cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, wrapErr(err, "find the release cutoff")
	}
	res, err := s.db.ExecContext(ctx, `
DELETE FROM releases WHERE id IN (
	SELECT id FROM releases WHERE project_id = ? AND id <= ?
	ORDER BY id
	LIMIT ?
)`, projectID, cutoff, limit)
	if err != nil {
		return 0, wrapErr(err, "prune releases")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, wrapErr(err, "prune releases")
	}
	return n, nil
}
