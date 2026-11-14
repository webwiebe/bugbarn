package storage

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func formatID(prefix string, value int64) string {
	return fmt.Sprintf("%s%d", prefix, value)
}

// parseID reads the row id out of a prefixed public id such as "alert-7". An id
// that does not parse is invalid input (400), the same answer a malformed issue
// id gets, so no handler needs its own check.
func parseID(prefix, value string) (int64, error) {
	value = strings.TrimSpace(value)
	kind := strings.TrimSuffix(prefix, "-")
	if !strings.HasPrefix(value, prefix) {
		return 0, apperr.InvalidInput(fmt.Sprintf("invalid %s id %q", kind, value), nil)
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
	if err != nil {
		return 0, apperr.InvalidInput(fmt.Sprintf("invalid %s id %q", kind, value), err)
	}
	return n, nil
}

func formatIssueID(prefix string, number int) string {
	return fmt.Sprintf("%s-%d", prefix, number)
}

// displayIssueID returns the Jira-style issue ID when the project has a prefix
// and the issue has a positive number, otherwise the legacy "issue-<rowID>"
// form. Centralizes the prefix/number-vs-legacy branch used by every issue and
// event scan path.
func displayIssueID(prefix string, number int, fallbackRowID int64) string {
	if prefix != "" && number > 0 {
		return formatIssueID(prefix, number)
	}
	return formatID(issueIDPrefix, fallbackRowID)
}

// parseIssueID splits a Jira-style issue ID like "BW-42" into prefix and number.
// Also handles legacy "issue-000042" format for backward compatibility.
func parseIssueID(value string) (prefix string, number int, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", 0, apperr.InvalidInput("empty issue id", nil)
	}
	// Legacy format: issue-NNNNNN
	if strings.HasPrefix(value, issueIDPrefix) {
		n, err := strconv.ParseInt(strings.TrimPrefix(value, issueIDPrefix), 10, 64)
		if err != nil {
			return "", 0, apperr.InvalidInput(fmt.Sprintf("invalid legacy issue id %q", value), err)
		}
		return "", int(n), nil
	}
	idx := strings.LastIndex(value, "-")
	if idx <= 0 {
		return "", 0, apperr.InvalidInput(fmt.Sprintf("invalid issue id %q", value), nil)
	}
	n, err := strconv.Atoi(value[idx+1:])
	if err != nil {
		return "", 0, apperr.InvalidInput(fmt.Sprintf("invalid issue id %q", value), err)
	}
	return value[:idx], n, nil
}

// deriveIssuePrefix generates a short uppercase prefix from a project slug.
// e.g. "bugbarn-web" → "BW", "my-service" → "MS", "frontend" → "FRO"
func deriveIssuePrefix(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "DEF"
	}
	parts := strings.FieldsFunc(slug, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '.'
	})
	if len(parts) == 1 {
		upper := strings.ToUpper(parts[0])
		if len(upper) <= 3 {
			return upper
		}
		return upper[:3]
	}
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteByte(p[0])
		}
	}
	return strings.ToUpper(b.String())
}
