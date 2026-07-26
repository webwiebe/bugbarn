package alert

import (
	"fmt"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// sparkBar is a single hourly bar in the 24h event-volume sparkline.
type sparkBar struct {
	Height int  // pixel height, 2..40
	Count  int  // events in this hour
	Zero   bool // true when Count == 0 (rendered faint)
}

// issueURL builds the absolute dashboard URL for an issue. It returns "" when
// no public URL is configured (so callers omit the link rather than emit a
// relative href that mail clients mis-resolve, e.g. to x-webdoc://). A bare
// host without a scheme is upgraded to https://.
func issueURL(publicURL, issueID string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if base == "" {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return base + "/app/#/issues/" + issueID
}

// alertSubject builds the alert email subject. The project comes first so it is
// obvious at a glance which project broke — these mails fan in from every
// project, and the issue title alone rarely says. The old subject led with
// "[BugBarn · <env>] Admin notifications:", which is two pieces of information
// the reader already has (it arrived from BugBarn, and "Admin notifications" is
// an internal rule label) before the part they actually need.
//
// A user-defined rule keeps its name — the user chose it, so it carries intent.
// The environment trails in brackets so production and staging stay tellable
// apart without pushing the subject line off the left edge.
//
// Every component is optional: the project slug is populated best-effort by the
// write path and can be empty, and env is unset outside deployed environments.
func alertSubject(env string, rule Rule, issue domain.Issue) string {
	var b strings.Builder
	if slug := strings.TrimSpace(issue.ProjectSlug); slug != "" {
		b.WriteString(slug)
		b.WriteString(": ")
	}
	if name := strings.TrimSpace(rule.Name); name != "" && !rule.isAdmin() {
		b.WriteString(name)
		b.WriteString(": ")
	}
	b.WriteString(issue.Title)
	if env = strings.TrimSpace(env); env != "" {
		b.WriteString(" [")
		b.WriteString(env)
		b.WriteString("]")
	}
	return b.String()
}

// buildSparkline scales a 24h hourly count array to fixed-height bars.
func buildSparkline(counts [24]int) []sparkBar {
	max := 0
	for _, c := range counts {
		if c > max {
			max = c
		}
	}
	bars := make([]sparkBar, len(counts))
	for i, c := range counts {
		b := sparkBar{Count: c, Zero: c == 0}
		switch {
		case c == 0:
			b.Height = 2
		case max <= 0:
			b.Height = 2
		default:
			b.Height = 4 + (c*36)/max // 4..40px
		}
		bars[i] = b
	}
	return bars
}

// messageOf returns the most descriptive error message available on an event.
func messageOf(ev event.Event) string {
	if m := strings.TrimSpace(ev.Exception.Message); m != "" {
		return m
	}
	return strings.TrimSpace(ev.Message)
}

// locationOf returns a "function (file:line)" culprit from the top stack frame.
func locationOf(ev event.Event) string {
	for _, f := range ev.Exception.Stacktrace {
		fn := strings.TrimSpace(f.Function)
		file := strings.TrimSpace(f.File)
		if fn == "" && file == "" {
			continue
		}
		switch {
		case file == "":
			return fn
		case f.Line > 0:
			loc := fmt.Sprintf("%s:%d", file, f.Line)
			if fn != "" {
				return fn + " (" + loc + ")"
			}
			return loc
		default:
			if fn != "" {
				return fn + " (" + file + ")"
			}
			return file
		}
	}
	return ""
}

// firstAttr returns the first non-empty value found across an event's
// attributes and resource maps for any of the candidate keys.
func firstAttr(ev event.Event, keys ...string) string {
	for _, m := range []map[string]any{ev.Attributes, ev.Resource} {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" && s != "<nil>" {
					return s
				}
			}
		}
	}
	return ""
}

// formatWhen renders a timestamp for display, or "" if it is the zero value.
func formatWhen(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}
