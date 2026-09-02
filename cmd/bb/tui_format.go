package main

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// maxValueWidth caps a single attribute/breadcrumb value. Event payloads carry
// arbitrary user data — one multi-kilobyte field would otherwise push the rest
// of the occurrence off the screen.
const maxValueWidth = 400

// minWrapWidth is the narrowest column worth wrapping into; below it the
// terminal is so cramped that wrapping only shreds the text.
const minWrapWidth = 24

// fieldPrefixWidth is the column writeField's values start at: two spaces of
// indent, an 11-cell label column, and the space after it. fieldIndent is that
// same column as padding, for continuation lines of a wrapped field value.
const fieldPrefixWidth = 14

const fieldIndent = "              "

// truncate shortens s to at most width runes, appending an ellipsis. It slices
// on rune boundaries so a multi-byte title is never cut in half.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// writeField renders one aligned "label: value" line, skipping empty values so
// sparse events don't render a wall of blanks.
func writeField(b *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "  %s %s\n", labelStyle.Render(fmt.Sprintf("%-11s", label+":")), value)
}

// writeHeading starts a new section of the detail pane.
func writeHeading(b *strings.Builder, text string) {
	b.WriteString("\n" + sectionStyle.Render(text) + "\n")
}

// writeSortedMap renders a string-keyed map with stable key ordering, so the
// same event always renders identically between refreshes.
func writeSortedMap(b *strings.Builder, values map[string]any, indent string, width int) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		value := truncate(formatValue(values[k]), maxValueWidth)
		prefix := indent + k + ": "
		fmt.Fprintf(b, "%s%s %s\n", indent, keyStyle.Render(k+":"),
			wrap(value, indent+"  ", width-len(prefix)))
	}
}

// wrap word-wraps text into a column `avail` wide and indents every
// continuation line with contIndent, so a long value stays visually attached to
// its label instead of running back to the left margin.
//
// avail is the width available to the text itself: callers subtract whatever
// prefix they already printed on the first line, so no line can overflow the
// pane and get wrapped a second time at the viewport edge (which is what
// produces the ragged column-0 continuations).
func wrap(text, contIndent string, avail int) string {
	if avail < minWrapWidth || len([]rune(text)) <= avail {
		return text
	}
	lines := strings.Split(lipgloss.NewStyle().Width(avail).Render(text), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
		if i > 0 {
			lines[i] = contIndent + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// renderPerLine applies a style to each line of text separately. Handing a
// multi-line string to lipgloss pads every line out to the widest one, which
// would silently re-introduce the over-wide lines wrap just removed.
func renderPerLine(style lipgloss.Style, text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// formatValue renders a JSON-decoded value compactly: strings bare, whole
// numbers without the float64 tail, everything else re-encoded as JSON.
func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// formatStamp renders an absolute local timestamp, or "" when the time is unset.
func formatStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// formatStampAgo pairs an absolute timestamp with its relative age, which is
// what you actually want when triaging ("is this still happening?").
func formatStampAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s (%s)", formatStamp(t), timeAgo(t))
}

// clockTime renders a breadcrumb timestamp as wall-clock time, falling back to
// the raw string when the SDK sent something other than RFC 3339.
func clockTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("15:04:05.000")
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}
