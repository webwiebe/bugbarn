package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

// upsertIssue is the ingest write path: it finds-or-creates the issue for an
// event's fingerprint and applies regression/mute transitions in one tx.
//
//nolint:gocognit,gocyclo,funlen // legacy ingest write path; complexity is tracked for a dedicated refactor.
func (s *core) upsertIssue(ctx context.Context, projectID int64, processed worker.ProcessedEvent) (Issue, int64, bool, error) {
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
		return Issue{}, 0, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Issue{}, 0, false, err
	}
	defer tx.Rollback()

	var (
		id                   int64
		issue                Issue
		existing             bool
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
	)

	var existingIssueNumber int
	err = tx.QueryRowContext(ctx, `
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
		&id,
		&issue.Fingerprint,
		&storedMaterial,
		&storedExplanation,
		&issue.Title,
		&issue.NormalizedTitle,
		&issue.ExceptionType,
		&status,
		&muteMode,
		&resolvedAt,
		&reopenedAt,
		&lastRegressedAt,
		&regressionCount,
		&firstSeen,
		&lastSeen,
		&eventCount,
		&storedRepresentative,
		&existingIssueNumber,
	)
	switch {
	case err == nil:
		existing = true
	case errors.Is(err, sql.ErrNoRows):
		existing = false
	case err != nil:
		return Issue{}, 0, false, err
	}

	if existing {
		parsedFirstSeen, err := parseTime(firstSeen)
		if err != nil {
			return Issue{}, 0, false, err
		}
		parsedLastSeen, err := parseTime(lastSeen)
		if err != nil {
			return Issue{}, 0, false, err
		}
		issue.IssueNumber = existingIssueNumber
		issue.FirstSeen = parsedFirstSeen
		issue.LastSeen = parsedLastSeen
		issue.EventCount = eventCount + 1
		issue.Status = status
		issue.RegressionCount = regressionCount
		issue.FingerprintMaterial = storedMaterial
		issue.ResolvedAt, _ = parseTime(resolvedAt)
		issue.ReopenedAt, _ = parseTime(reopenedAt)
		issue.LastRegressedAt, _ = parseTime(lastRegressedAt)
		if err := unmarshalStringSlice(storedExplanation, &issue.FingerprintExplanation); err != nil {
			return Issue{}, 0, false, err
		}
		if err := json.Unmarshal(storedRepresentative, &issue.RepresentativeEvent); err != nil {
			return Issue{}, 0, false, err
		}
		if issue.FingerprintMaterial == "" {
			issue.FingerprintMaterial = material
		}
		if len(issue.FingerprintExplanation) == 0 {
			issue.FingerprintExplanation = explanation
		}
		if seenAt.After(issue.LastSeen) {
			issue.LastSeen = seenAt
		}

		issue.MuteMode = muteMode
		regressed := issue.Status == "resolved"

		if existing && muteMode == "until_regression" && (issue.Status == "resolved" || issue.Status == "muted") {
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
			return Issue{}, 0, false, err
		}

		if err := tx.Commit(); err != nil {
			return Issue{}, 0, false, err
		}
		issue.Fingerprint = fingerprintValue
		issue.RepresentativeEvent = evt
		// Set display ID for existing issue.
		var existingPrefix string
		_ = s.readDB().QueryRowContext(ctx, `SELECT issue_prefix FROM projects WHERE id = ?`, projectID).Scan(&existingPrefix)
		issue.ID = displayIssueID(existingPrefix, existingIssueNumber, id)
		return issue, id, regressed, nil
	}

	// Atomically increment the project's issue counter.
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET issue_counter = issue_counter + 1 WHERE id = ?`, projectID); err != nil {
		return Issue{}, 0, false, err
	}
	var issueNumber int
	var issuePrefix string
	if err := tx.QueryRowContext(ctx,
		`SELECT issue_counter, issue_prefix FROM projects WHERE id = ?`, projectID).Scan(&issueNumber, &issuePrefix); err != nil {
		return Issue{}, 0, false, err
	}

	issue = Issue{
		Fingerprint:            fingerprintValue,
		FingerprintMaterial:    material,
		FingerprintExplanation: explanation,
		Title:                  title,
		NormalizedTitle:        normalizedTitle,
		ExceptionType:          exceptionType,
		Status:                 "unresolved",
		FirstSeen:              seenAt,
		LastSeen:               seenAt,
		EventCount:             1,
		RepresentativeEvent:    evt,
		IssueNumber:            issueNumber,
	}

	res, err := tx.ExecContext(ctx, `
INSERT INTO issues (
	project_id,
	fingerprint,
	fingerprint_material,
	fingerprint_explanation_json,
	title,
	normalized_title,
	exception_type,
	status,
	first_seen,
	last_seen,
	event_count,
	representative_event_json,
	issue_number
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		fingerprintValue,
		material,
		mustMarshalStrings(explanation),
		title,
		normalizedTitle,
		exceptionType,
		"unresolved",
		formatTime(seenAt),
		formatTime(seenAt),
		1,
		representative,
		issueNumber,
	)
	if err != nil {
		return Issue{}, 0, false, err
	}

	id, err = res.LastInsertId()
	if err != nil {
		return Issue{}, 0, false, err
	}
	issue.ID = displayIssueID(issuePrefix, issueNumber, id)

	if err := tx.Commit(); err != nil {
		return Issue{}, 0, false, err
	}

	return issue, id, false, nil
}

func issueSeenAt(evt event.Event) time.Time {
	if !evt.ObservedAt.IsZero() {
		return evt.ObservedAt.UTC()
	}
	if !evt.ReceivedAt.IsZero() {
		return evt.ReceivedAt.UTC()
	}
	return time.Now().UTC()
}

func issueDetails(evt event.Event) (title, normalizedTitle, exceptionType string) {
	exceptionType = strings.TrimSpace(evt.Exception.Type)
	message := strings.TrimSpace(evt.Exception.Message)
	if message == "" {
		message = strings.TrimSpace(evt.Message)
	}

	// When exception is empty, fall back to rawScrubbed data. This handles
	// browser errors (promise rejections, cross-origin) that arrive with
	// exception: {} but have details in rawScrubbed.
	if exceptionType == "" && message == "" {
		if raw := rawScrubbedFallback(evt.RawScrubbed); raw.name != "" || raw.message != "" {
			exceptionType = strings.TrimSpace(raw.name)
			message = strings.TrimSpace(raw.message)
		}
	}

	switch {
	case exceptionType != "" && message != "":
		title = exceptionType + ": " + message
	case exceptionType != "":
		title = exceptionType
	default:
		title = message
	}

	const maxTitleLen = 512
	if len(title) > maxTitleLen {
		title = title[:maxTitleLen]
	}

	normalizedTitle = normalizeTitle(title)
	if len(normalizedTitle) > maxTitleLen {
		normalizedTitle = normalizedTitle[:maxTitleLen]
	}
	return title, normalizedTitle, exceptionType
}

func normalizeTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = uuidPattern.ReplaceAllString(value, "<id>")
	value = ipv4Pattern.ReplaceAllString(value, "<ip>")
	value = hexAddress.ReplaceAllString(value, "<hex>")
	value = longNumber.ReplaceAllString(value, "<num>")
	value = pathNumber.ReplaceAllString(value, "/:num")
	value = whitespace.ReplaceAllString(value, " ")
	value = trimPunctuation.ReplaceAllString(value, "")
	return value
}
