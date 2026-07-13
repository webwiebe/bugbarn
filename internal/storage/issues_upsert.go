package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

// upsertIssue is the ingest write path: it finds-or-creates the issue for an
// event's fingerprint and applies regression/mute transitions in one tx.
func (s *core) upsertIssue(ctx context.Context, projectID int64, processed worker.ProcessedEvent) (Issue, int64, bool, error) {
	in, err := prepareUpsertInputs(processed)
	if err != nil {
		return Issue{}, 0, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Issue{}, 0, false, err
	}
	defer tx.Rollback()

	row, existing, err := findExistingIssue(ctx, tx, projectID, in.fingerprintValue)
	if err != nil {
		return Issue{}, 0, false, err
	}

	if existing {
		return s.updateExistingIssue(ctx, tx, existingIssueParams{
			projectID:            projectID,
			id:                   row.id,
			issue:                row.issue,
			eventCount:           row.eventCount,
			firstSeen:            row.firstSeen,
			lastSeen:             row.lastSeen,
			resolvedAt:           row.resolvedAt,
			reopenedAt:           row.reopenedAt,
			lastRegressedAt:      row.lastRegressedAt,
			regressionCount:      row.regressionCount,
			status:               row.status,
			muteMode:             row.muteMode,
			storedMaterial:       row.storedMaterial,
			storedExplanation:    row.storedExplanation,
			storedRepresentative: row.storedRepresentative,
			existingIssueNumber:  row.existingIssueNumber,
			material:             in.material,
			explanation:          in.explanation,
			seenAt:               in.seenAt,
			evt:                  in.evt,
			fingerprintValue:     in.fingerprintValue,
		})
	}

	return s.insertNewIssue(ctx, tx, newIssueParams{
		projectID:        projectID,
		fingerprintValue: in.fingerprintValue,
		material:         in.material,
		explanation:      in.explanation,
		title:            in.title,
		normalizedTitle:  in.normalizedTitle,
		exceptionType:    in.exceptionType,
		seenAt:           in.seenAt,
		evt:              in.evt,
		representative:   in.representative,
	})
}

// upsertInputs holds the derived event metadata used by both the find-existing
// and the create-new paths of upsertIssue.
type upsertInputs struct {
	evt              event.Event
	fingerprintValue string
	material         string
	explanation      []string
	title            string
	normalizedTitle  string
	exceptionType    string
	seenAt           time.Time
	representative   []byte
}

// prepareUpsertInputs derives the fingerprint, material, explanation, title and
// representative-event blob from the processed event, applying the same
// fallbacks the ingest path relied on.
func prepareUpsertInputs(processed worker.ProcessedEvent) (upsertInputs, error) {
	evt := processed.Event
	fingerprintValue := strings.TrimSpace(processed.Fingerprint)
	if fingerprintValue == "" {
		fingerprintValue = fingerprintFromEvent(evt)
	}
	material := strings.TrimSpace(processed.FingerprintMaterial)
	if material == "" {
		material = evt.FingerprintMaterial
	}
	explanation := processed.FingerprintExplanation
	if len(explanation) == 0 {
		explanation = evt.FingerprintExplanation
	}

	title, normalizedTitle, exceptionType := issueDetails(evt)
	seenAt := issueSeenAt(evt)
	representative, err := marshalEvent(evt)
	if err != nil {
		return upsertInputs{}, err
	}

	return upsertInputs{
		evt:              evt,
		fingerprintValue: fingerprintValue,
		material:         material,
		explanation:      explanation,
		title:            title,
		normalizedTitle:  normalizedTitle,
		exceptionType:    exceptionType,
		seenAt:           seenAt,
		representative:   representative,
	}, nil
}

// scannedIssueRow holds the columns scanned from the find-existing-issue query.
type scannedIssueRow struct {
	id                   int64
	issue                Issue
	eventCount           int
	firstSeen            string
	lastSeen             string
	resolvedAt           string
	reopenedAt           string
	lastRegressedAt      string
	regressionCount      int
	status               string
	muteMode             string
	storedMaterial       string
	storedExplanation    []byte
	storedRepresentative []byte
	existingIssueNumber  int
}

// findExistingIssue looks up an issue by (project, fingerprint) within the
// transaction. It returns the scanned row and existing=false (no error) when no
// matching issue is found.
func findExistingIssue(ctx context.Context, tx *sql.Tx, projectID int64, fingerprintValue string) (scannedIssueRow, bool, error) {
	var row scannedIssueRow
	err := tx.QueryRowContext(ctx, `
SELECT
	id,
	fingerprint,
	fingerprint_material,
	fingerprint_explanation_json,
	title,
	normalized_title,
	exception_type,
	status,
	mute_mode,
	resolved_at,
	reopened_at,
	last_regressed_at,
	regression_count,
	first_seen,
	last_seen,
	event_count,
	representative_event_json,
	issue_number
FROM issues
WHERE project_id = ? AND fingerprint = ?`,
		projectID,
		fingerprintValue,
	).Scan(
		&row.id,
		&row.issue.Fingerprint,
		&row.storedMaterial,
		&row.storedExplanation,
		&row.issue.Title,
		&row.issue.NormalizedTitle,
		&row.issue.ExceptionType,
		&row.status,
		&row.muteMode,
		&row.resolvedAt,
		&row.reopenedAt,
		&row.lastRegressedAt,
		&row.regressionCount,
		&row.firstSeen,
		&row.lastSeen,
		&row.eventCount,
		&row.storedRepresentative,
		&row.existingIssueNumber,
	)
	switch {
	case err == nil:
		return row, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return row, false, nil
	default:
		return row, false, err
	}
}

// applyRegressionTransition decides whether a new event on an existing issue
// constitutes a regression and mutates the issue's status/mute/regression fields
// accordingly. It returns whether a regression occurred (which drives the UPDATE).
func applyRegressionTransition(issue *Issue, muteMode string, seenAt time.Time) bool {
	regressed := issue.Status == "resolved"

	if muteMode == "until_regression" && (issue.Status == "resolved" || issue.Status == "muted") {
		// A new event on a muted-until-regression issue triggers a regression.
		regressed = true
	}

	if regressed {
		if muteMode == "forever" {
			// Keep muted; don't change status.
			regressed = false
		} else {
			newStatus := "unresolved"
			if muteMode == "until_regression" {
				newStatus = "regressed"
				issue.MuteMode = "" // unmute now that regression occurred
			}
			issue.Status = newStatus
			issue.RegressionCount++
			issue.ReopenedAt = seenAt
			issue.LastRegressedAt = seenAt
		}
	}
	return regressed
}

type existingIssueParams struct {
	projectID            int64
	id                   int64
	issue                Issue
	eventCount           int
	firstSeen            string
	lastSeen             string
	resolvedAt           string
	reopenedAt           string
	lastRegressedAt      string
	regressionCount      int
	status               string
	muteMode             string
	storedMaterial       string
	storedExplanation    []byte
	storedRepresentative []byte
	existingIssueNumber  int
	material             string
	explanation          []string
	seenAt               time.Time
	evt                  event.Event
	fingerprintValue     string
}

// updateExistingIssue computes the regression/mute transition for an existing
// issue and applies the UPDATE within the open transaction.
func (s *core) updateExistingIssue(ctx context.Context, tx *sql.Tx, p existingIssueParams) (Issue, int64, bool, error) {
	issue, err := hydrateExistingIssue(p)
	if err != nil {
		return Issue{}, 0, false, err
	}

	issue.MuteMode = p.muteMode
	regressed := applyRegressionTransition(&issue, p.muteMode, p.seenAt)

	if err := updateIssueRow(ctx, tx, issue, regressed, p.id, p.projectID); err != nil {
		return Issue{}, 0, false, err
	}

	if err := tx.Commit(); err != nil {
		return Issue{}, 0, false, err
	}
	issue.Fingerprint = p.fingerprintValue
	issue.RepresentativeEvent = p.evt
	// Set display ID for existing issue.
	var existingPrefix string
	prefixRow := s.readDB().QueryRowContext(ctx,
		`SELECT issue_prefix FROM projects WHERE id = ?`, p.projectID)
	if err := prefixRow.Scan(&existingPrefix); err != nil {
		slog.ErrorContext(ctx, "storage: failed to look up issue prefix; display id will be corrupted",
			"issue_id", p.id, "project_id", p.projectID, "error", err)
	}
	issue.ID = displayIssueID(existingPrefix, p.existingIssueNumber, p.id)
	return issue, p.id, regressed, nil
}

// hydrateExistingIssue reconstructs the in-memory Issue from the scanned row
// columns, parsing timestamps and decoding the stored JSON fields.
func hydrateExistingIssue(p existingIssueParams) (Issue, error) {
	issue := p.issue
	parsedFirstSeen, err := parseTime(p.firstSeen)
	if err != nil {
		return Issue{}, err
	}
	parsedLastSeen, err := parseTime(p.lastSeen)
	if err != nil {
		return Issue{}, err
	}
	issue.IssueNumber = p.existingIssueNumber
	issue.FirstSeen = parsedFirstSeen
	issue.LastSeen = parsedLastSeen
	issue.EventCount = p.eventCount + 1
	issue.Status = p.status
	issue.RegressionCount = p.regressionCount
	issue.FingerprintMaterial = p.storedMaterial
	issue.ResolvedAt, _ = parseTime(p.resolvedAt)
	issue.ReopenedAt, _ = parseTime(p.reopenedAt)
	issue.LastRegressedAt, _ = parseTime(p.lastRegressedAt)
	if err := unmarshalStringSlice(p.storedExplanation, &issue.FingerprintExplanation); err != nil {
		return Issue{}, err
	}
	if err := json.Unmarshal(p.storedRepresentative, &issue.RepresentativeEvent); err != nil {
		return Issue{}, err
	}
	if issue.FingerprintMaterial == "" {
		issue.FingerprintMaterial = p.material
	}
	if len(issue.FingerprintExplanation) == 0 {
		issue.FingerprintExplanation = p.explanation
	}
	if p.seenAt.After(issue.LastSeen) {
		issue.LastSeen = p.seenAt
	}
	return issue, nil
}

// updateIssueRow builds the dynamic UPDATE assignments (adding the regression
// columns when a regression occurred) and applies it within the transaction.
func updateIssueRow(ctx context.Context, tx *sql.Tx, issue Issue, regressed bool, id, projectID int64) error {
	assignments := []string{
		"last_seen = ?",
		"event_count = event_count + 1",
		"updated_at = CURRENT_TIMESTAMP",
	}
	args := []any{formatTime(issue.LastSeen)}
	if regressed {
		newStatus := issue.Status
		assignments = append(assignments,
			"status = ?",
			"mute_mode = ?",
			"reopened_at = ?",
			"last_regressed_at = ?",
			"regression_count = regression_count + 1",
		)
		args = append(args, newStatus, issue.MuteMode, formatTime(issue.ReopenedAt), formatTime(issue.LastRegressedAt))
	}
	args = append(args, id, projectID)

	if _, err := tx.ExecContext(ctx, `
UPDATE issues
SET `+strings.Join(assignments, ", ")+`
WHERE id = ? AND project_id = ?`,
		args...,
	); err != nil {
		return err
	}
	return nil
}
