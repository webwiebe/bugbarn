package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

type newIssueParams struct {
	projectID        int64
	fingerprintValue string
	material         string
	explanation      []string
	title            string
	normalizedTitle  string
	exceptionType    string
	seenAt           time.Time
	evt              event.Event
	representative   []byte
}

// insertNewIssue allocates the next issue number and INSERTs a new issue within
// the open transaction.
func (s *core) insertNewIssue(ctx context.Context, tx *sql.Tx, p newIssueParams) (Issue, int64, bool, error) {
	issueNumber, issuePrefix, err := allocateIssueNumber(ctx, tx, p.projectID)
	if err != nil {
		return Issue{}, 0, false, err
	}

	issue := Issue{
		Fingerprint:            p.fingerprintValue,
		FingerprintMaterial:    p.material,
		FingerprintExplanation: p.explanation,
		Title:                  p.title,
		NormalizedTitle:        p.normalizedTitle,
		ExceptionType:          p.exceptionType,
		Status:                 "unresolved",
		FirstSeen:              p.seenAt,
		LastSeen:               p.seenAt,
		EventCount:             1,
		RepresentativeEvent:    p.evt,
		IssueNumber:            issueNumber,
	}

	id, err := insertIssueRow(ctx, tx, p, issueNumber)
	if err != nil {
		return Issue{}, 0, false, err
	}
	issue.ID = displayIssueID(issuePrefix, issueNumber, id)

	if err := tx.Commit(); err != nil {
		return Issue{}, 0, false, err
	}

	return issue, id, false, nil
}

// insertIssueRow executes the INSERT for a new issue and returns its row id.
func insertIssueRow(ctx context.Context, tx *sql.Tx, p newIssueParams, issueNumber int) (int64, error) {
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
		p.projectID,
		p.fingerprintValue,
		p.material,
		mustMarshalStrings(p.explanation),
		p.title,
		p.normalizedTitle,
		p.exceptionType,
		"unresolved",
		formatTime(p.seenAt),
		formatTime(p.seenAt),
		1,
		p.representative,
		issueNumber,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// allocateIssueNumber atomically increments the project's issue counter and
// returns the new issue number along with the project's display prefix.
func allocateIssueNumber(ctx context.Context, tx *sql.Tx, projectID int64) (issueNumber int, issuePrefix string, err error) {
	// Atomically increment the project's issue counter.
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET issue_counter = issue_counter + 1 WHERE id = ?`, projectID); err != nil {
		return 0, "", err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT issue_counter, issue_prefix FROM projects WHERE id = ?`, projectID).Scan(&issueNumber, &issuePrefix); err != nil {
		return 0, "", err
	}
	return issueNumber, issuePrefix, nil
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
