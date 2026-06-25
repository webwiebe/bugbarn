package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
)

type fingerprintUpdate struct {
	id             int64
	projectID      int64
	newFingerprint string
	newMaterial    string
	newExplanation []string
}

func (s *core) migrateFingerprints(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, fingerprint, fingerprint_material, representative_event_json
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
		eventJSON   []byte
	}

	var issues []issueRow
	for rows.Next() {
		var r issueRow
		if err := rows.Scan(&r.id, &r.projectID, &r.fingerprint, &r.material, &r.eventJSON); err != nil {
			rows.Close()
			return err
		}
		issues = append(issues, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var updates []fingerprintUpdate
	for _, r := range issues {
		var evt event.Event
		if err := json.Unmarshal(r.eventJSON, &evt); err != nil {
			continue
		}
		snap := fingerprint.SnapshotFor(evt)
		fp := fingerprint.Fingerprint(evt)
		if fp == r.fingerprint {
			continue
		}
		updates = append(updates, fingerprintUpdate{
			id:             r.id,
			projectID:      r.projectID,
			newFingerprint: fp,
			newMaterial:    snap.Material,
			newExplanation: snap.Explanation,
		})
	}

	if len(updates) == 0 {
		return nil
	}

	slog.Info("recomputing fingerprints", "count", len(updates))

	const batchSize = 100
	for start := 0; start < len(updates); start += batchSize {
		end := start + batchSize
		if end > len(updates) {
			end = len(updates)
		}
		if err := s.applyFingerprintBatch(ctx, updates[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *core) applyFingerprintBatch(ctx context.Context, batch []fingerprintUpdate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, u := range batch {
		var keeperID int64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM issues
			WHERE project_id = ? AND fingerprint = ? AND id != ?`,
			u.projectID, u.newFingerprint, u.id,
		).Scan(&keeperID)

		explanationJSON, _ := json.Marshal(u.newExplanation)

		if err == nil {
			if err := mergeIntoKeeper(ctx, tx, u, keeperID); err != nil {
				return err
			}
		} else {
			if err := rewriteFingerprint(ctx, tx, u, string(explanationJSON)); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// mergeIntoKeeper folds issue u into the existing keeper issue that already
// carries the recomputed fingerprint, then deletes u.
func mergeIntoKeeper(ctx context.Context, tx *sql.Tx, u fingerprintUpdate, keeperID int64) error {
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
	slog.Info("merged issue", "from_id", u.id, "into_id", keeperID, "fingerprint", u.newFingerprint)
	return nil
}

// rewriteFingerprint updates issue u and its events in place with the
// recomputed fingerprint when no keeper issue exists to merge into.
func rewriteFingerprint(ctx context.Context, tx *sql.Tx, u fingerprintUpdate, explanationJSON string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE issues SET fingerprint = ?, fingerprint_material = ?, fingerprint_explanation_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		u.newFingerprint, u.newMaterial, explanationJSON, u.id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE events SET fingerprint = ?, fingerprint_material = ?
		WHERE issue_id = ?`,
		u.newFingerprint, u.newMaterial, u.id); err != nil {
		return err
	}
	return nil
}
