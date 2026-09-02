package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (m model) viewDetail() string {
	d := m.detail
	prefix := statusStyle(d.Status).Render(statusIcon(d.Status)) + " " + d.ID + " · "
	title := truncate(d.Title, max(10, m.width-lipgloss.Width(prefix)-6))
	header := headerStyle.Width(m.width).Render(prefix + title)

	m.viewport.Width = m.width
	m.viewport.Height = max(1, m.height-3)

	return lipgloss.JoinVertical(lipgloss.Left, header, m.viewport.View(), m.detailFooter())
}

func (m model) detailFooter() string {
	nav := helpItem("←/→", "occurrence "+m.occurrencePosition())
	if len(m.events) < 2 {
		nav = helpStyle.Render("occurrence " + m.occurrencePosition())
	}
	return footerStyle.Width(m.width).Render(
		helpItem("esc", "back") + "  " +
			helpItem("↑/↓", "scroll") + "  " +
			nav + "  " +
			helpItem("v", "vibe") + "  " +
			helpItem("r", "resolve") + "  " +
			helpItem("q", "quit"))
}

// occurrencePosition reads as "3/50", or names the fallback when the issue's
// own events are gone (retention) or still loading.
func (m model) occurrencePosition() string {
	switch {
	case m.eventsLoading:
		return "loading…"
	case len(m.events) == 0:
		return "representative"
	default:
		return fmt.Sprintf("%d/%d", m.eventIdx+1, len(m.events))
	}
}

// refreshDetail re-renders the detail pane into the viewport. It takes a
// pointer receiver because viewport.SetContent mutates.
//
// The content is hard-wrapped to the viewport width first. The viewport counts
// the lines it was given but wraps them again at render time, so an over-wide
// line — a long attribute value, a deep stack path — would silently push the
// bottom of the pane out of reach instead of scrolling into it.
func (m *model) refreshDetail() {
	content := m.renderDetailContent()
	if w := m.viewport.Width; w > 0 {
		content = lipgloss.NewStyle().Width(w).Render(content)
	}
	m.viewport.SetContent(content)
}

// currentEvent returns the occurrence on screen: the selected one of the
// issue's recent events, or — while those load, and once retention has expired
// them — a stand-in built from the issue's stored representative event, so both
// cases render through exactly one path.
func (m model) currentEvent() domain.Event {
	if m.eventIdx >= 0 && m.eventIdx < len(m.events) {
		return m.events[m.eventIdx]
	}
	d := m.detail
	return domain.Event{
		IssueID:                d.ID,
		Fingerprint:            d.Fingerprint,
		FingerprintMaterial:    d.FingerprintMaterial,
		FingerprintExplanation: d.FingerprintExplanation,
		ReceivedAt:             d.LastSeen,
		ObservedAt:             d.RepresentativeEvent.ObservedAt,
		Severity:               d.RepresentativeEvent.Severity,
		Message:                d.RepresentativeEvent.Message,
		Payload:                d.RepresentativeEvent,
	}
}

func (m model) renderDetailContent() string {
	var b strings.Builder
	width := m.contentWidth()
	m.writeIssueSection(&b)
	m.writeOccurrenceNotice(&b)
	writeOccurrenceSections(&b, m.currentEvent(), width)
	writeGroupingSection(&b, m.detail, width)
	return b.String()
}

// contentWidth is the column budget free-text values wrap to. It falls back to
// a sane default before the first WindowSizeMsg has sized the viewport.
func (m model) contentWidth() int {
	if m.viewport.Width > 0 {
		return m.viewport.Width
	}
	return 80
}

func (m model) writeIssueSection(b *strings.Builder) {
	d := m.detail
	b.WriteString(sectionStyle.Render("Issue") + "\n")
	writeField(b, "ID", d.ID)
	writeField(b, "Status", statusStyle(d.Status).Render(d.Status)+mutedSuffix(d))
	writeField(b, "Project", projectStyle.Render(d.ProjectSlug))
	writeField(b, "Events", fmt.Sprintf("%d", d.EventCount))
	writeField(b, "First seen", timeStyle.Render(formatStampAgo(d.FirstSeen)))
	writeField(b, "Last seen", timeStyle.Render(formatStampAgo(d.LastSeen)))
	if d.RegressionCount > 0 {
		writeField(b, "Regressed", fmt.Sprintf("%d× (last %s)", d.RegressionCount, formatStampAgo(d.LastRegressedAt)))
	}
	writeField(b, "Resolved", timeStyle.Render(formatStamp(d.ResolvedAt)))
	if d.ExceptionType != "" {
		writeField(b, "Exception", lipgloss.NewStyle().Foreground(errorColor).Render(d.ExceptionType))
	}
}

func mutedSuffix(d domain.Issue) string {
	if d.MuteMode == "" {
		return ""
	}
	return dimStyle.Render(" (" + d.MuteMode + ")")
}

// writeOccurrenceNotice explains which occurrence the sections below describe,
// and says plainly when it is a fallback rather than a real stored event.
func (m model) writeOccurrenceNotice(b *strings.Builder) {
	switch {
	case m.eventsLoading:
		writeHeading(b, "Occurrence")
		b.WriteString("  " + dimStyle.Render("loading recent occurrences…") + "\n")
	case m.eventsErr != nil:
		writeHeading(b, "Occurrence (representative)")
		notice := wrap("could not load recent occurrences: "+m.eventsErr.Error(), "  ", m.contentWidth()-2)
		b.WriteString("  " + renderPerLine(lipgloss.NewStyle().Foreground(errorColor), notice) + "\n")
	case len(m.events) == 0:
		writeHeading(b, "Occurrence (representative)")
		b.WriteString("  " + dimStyle.Render("no stored events — showing the issue's representative event") + "\n")
	default:
		writeHeading(b, fmt.Sprintf("Occurrence %d of %d", m.eventIdx+1, len(m.events)))
	}
}

func writeGroupingSection(b *strings.Builder, d domain.Issue, width int) {
	if d.Fingerprint == "" && len(d.FingerprintExplanation) == 0 {
		return
	}
	writeHeading(b, "Grouping")
	writeField(b, "Fingerprint", dimStyle.Render(d.Fingerprint))
	for _, line := range d.FingerprintExplanation {
		fmt.Fprintf(b, "  %s\n", wrap(line, "    ", width-4))
	}
	if d.FingerprintMaterial != "" {
		material := wrap(truncate(d.FingerprintMaterial, maxValueWidth), fieldIndent, width-fieldPrefixWidth)
		writeField(b, "Material", renderPerLine(dimStyle, material))
	}
}
