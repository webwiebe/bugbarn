package domain

import (
	"regexp"
	"strings"
	"time"
)

// An environment (production, staging, testing, e2e, dev) is an attribute of the
// data, never a project of its own (bugbarn#282). These helpers keep that rule
// in one place.

var environmentName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// environmentSuffixes maps a project-name suffix to the canonical environment
// it stands for.
var environmentSuffixes = map[string]string{
	"prod":        "production",
	"production":  "production",
	"stage":       "staging",
	"staging":     "staging",
	"test":        "testing",
	"testing":     "testing",
	"e2e":         "e2e",
	"dev":         "dev",
	"development": "dev",
}

// NormalizeEnvironment lowercases and validates an environment name. ok is false
// for an empty or malformed name.
func NormalizeEnvironment(name string) (string, bool) {
	env := strings.ToLower(strings.TrimSpace(name))
	if !environmentName.MatchString(env) {
		return "", false
	}
	return env, true
}

// EnvironmentSuffix reports whether a project slug or name ends in an environment
// suffix such as "-staging" or "-test", returning the name without the suffix and
// the canonical environment. Only a hyphen or underscore separates the suffix,
// so a display name like "Read Only Test" is not one. A name that is only an environment word ("staging")
// carries no product and is not reported.
func EnvironmentSuffix(name string) (base, env string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	cut := strings.LastIndexAny(lower, "-_")
	if cut <= 0 || cut == len(lower)-1 {
		return "", "", false
	}
	canonical, found := environmentSuffixes[lower[cut+1:]]
	if !found {
		return "", "", false
	}
	return strings.TrimSpace(name[:cut]), canonical, true
}

// MergeCounts is what one merge moved, per kind.
type MergeCounts struct {
	IssuesMoved        int `json:"issues_moved"`
	IssuesFolded       int `json:"issues_folded"`
	Events             int `json:"events"`
	Logs               int `json:"logs"`
	Releases           int `json:"releases"`
	Alerts             int `json:"alerts"`
	SourceMaps         int `json:"source_maps"`
	AnalyticsPageviews int `json:"analytics_pageviews"`
	APIKeys            int `json:"api_keys_redirected"`
	Aliases            int `json:"aliases_redirected"`
}

// MergeResult is the record of one merge, as stored in project_merges.
type MergeResult struct {
	ID                int64       `json:"id"`
	Source            string      `json:"source"`
	Target            string      `json:"target"`
	Environment       string      `json:"environment"`
	IssueNumberOffset int         `json:"issue_number_offset"`
	Counts            MergeCounts `json:"counts"`
	MergedAt          time.Time   `json:"merged_at"`
}
