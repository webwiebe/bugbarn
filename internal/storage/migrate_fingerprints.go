package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"regexp"
)

var sourceLocationPattern = regexp.MustCompile(`:\d+(?::\d+)+`)

func (s *Store) migrateFingerprints(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, fingerprint, fingerprint_material
		FROM issues
		WHERE fingerprint_material != ''`)
	if err != nil {
		return err
	}

	type issueRow struct {
		id          int64
		projectID   int64
		fingerprint string
		material    string
	}

	var issues []issueRow
	for rows.Next() {
		var r issueRow
		if err := rows.Scan(&r.id, &r.projectID, &r.fingerprint, &r.material); err != nil {
			rows.Close()
			return err
		}
		issues = append(issues, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Recompute fingerprints and find issues that need updating.
	type update struct {
		id             int64
		projectID      int64
		newFingerprint string
		newMaterial    string
	}
	var updates []update
	for _, r := range issues {
		newMaterial := sourceLocationPattern.ReplaceAllString(r.material, ":<loc>")
		if newMaterial == r.material {
			continue
		}
		sum := sha256.Sum256([]byte(newMaterial))
		updates = append(updates, update{
			id:             r.id,
			projectID:      r.projectID,
			newFingerprint: hex.EncodeToString(sum[:]),
			newMaterial:    newMaterial,
		})
	}

	if len(updates) == 0 {
		return nil
	}

	log.Printf("[migrate] recomputing %d fingerprints with source-location normalization", len(updates))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, u := range updates {
		// Check if an issue with the new fingerprint already exists for this project.
		var keeperID int64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM issues
			WHERE project_id = ? AND fingerprint = ? AND id != ?`,
			u.projectID, u.newFingerprint, u.id,
		).Scan(&keeperID)

		if err == nil {
			// Duplicate found — merge this issue into the keeper.
			if _, err := tx.ExecContext(ctx, `
				UPDATE events SET issue_id = ? WHERE issue_id = ?`,
				keeperID, u.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE event_facets SET issue_id = ? WHERE issue_id = ?`,
				keeperID, u.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE issues SET
					event_count = (SELECT COUNT(*) FROM events WHERE issue_id = ?),
					first_seen = (SELECT MIN(observed_at) FROM events WHERE issue_id = ?),
					last_seen = (SELECT MAX(observed_at) FROM events WHERE issue_id = ?),
					updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`,
				keeperID, keeperID, keeperID, keeperID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM issues WHERE id = ?`, u.id); err != nil {
				return err
			}
			log.Printf("[migrate] merged issue %d into %d (fingerprint %s)", u.id, keeperID, u.newFingerprint)
		} else {
			// No duplicate — just update the fingerprint.
			if _, err := tx.ExecContext(ctx, `
				UPDATE issues SET fingerprint = ?, fingerprint_material = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`,
				u.newFingerprint, u.newMaterial, u.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE events SET fingerprint = ?, fingerprint_material = ?
				WHERE issue_id = ?`,
				u.newFingerprint, u.newMaterial, u.id); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}
