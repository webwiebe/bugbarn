package main

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// maxBreadcrumbs bounds the breadcrumb trail. SDKs ship up to a few hundred;
// the most recent ones are the ones that explain the failure.
const maxBreadcrumbs = 40

// loggedAsLabel prefixes the log message an event was captured from.
const loggedAsLabel = "logged as:"

// breadcrumbPrefixWidth is the fixed part of a breadcrumb line before its
// message: indent, "15:04:05.000", the 5-cell level column, and the separators.
const breadcrumbPrefixWidth = 22

// writeOccurrenceSections renders everything BugBarn stored about a single
// event: when it happened, what it was, where it came from, who hit it, and the
// context the SDK attached. width is the pane width free-text values wrap to.
func writeOccurrenceSections(b *strings.Builder, ev domain.Event, width int) {
	writeEventMeta(b, ev)
	writeExceptionSection(b, ev, width)
	writeStackSection(b, ev.Payload.Exception)
	writeUserSection(b, ev.Payload.User)
	writeBreadcrumbsSection(b, ev.Payload.Breadcrumbs, width)
	writeMapSection(b, "Attributes", ev.Payload.Attributes, width)
	writeMapSection(b, "Resource", ev.Payload.Resource, width)
}

func writeEventMeta(b *strings.Builder, ev domain.Event) {
	writeField(b, "Event", dimStyle.Render(ev.ID))
	if ev.Severity != "" {
		writeField(b, "Severity", severityStyle(ev.Severity).Render(ev.Severity))
	}
	writeField(b, "Received", timeStyle.Render(formatStampAgo(ev.ReceivedAt)))
	// Ingest lag is normally sub-second and uninteresting; only call out an
	// observed time that actually lags behind when the event reached us.
	if lag := ev.ReceivedAt.Sub(ev.ObservedAt); !ev.ObservedAt.IsZero() && lag.Abs() >= time.Second {
		writeField(b, "Observed", timeStyle.Render(fmt.Sprintf("%s (%s before ingest)",
			formatStamp(ev.ObservedAt), lag.Round(time.Second))))
	}
	if ev.Regressed {
		writeField(b, "Regression", severityWarn.Render("this event reopened the issue"))
	}
	writeField(b, "SDK", ev.Payload.SDKName)
	writeField(b, "Trace", dimStyle.Render(traceRef(ev.Payload)))
}

// traceRef renders the trace/span pair that links an event to its distributed
// trace, so it can be looked up in the tracing backend.
func traceRef(p event.Event) string {
	if p.TraceID == "" {
		return ""
	}
	if p.SpanID == "" {
		return p.TraceID
	}
	return p.TraceID + "  span=" + p.SpanID
}

func writeExceptionSection(b *strings.Builder, ev domain.Event, width int) {
	exc := ev.Payload.Exception
	if exc.Type == "" && exc.Message == "" && ev.Message == "" {
		return
	}
	writeHeading(b, "Exception")
	if exc.Type != "" {
		fmt.Fprintf(b, "  %s\n", severityError.Render(exc.Type))
	}
	if exc.Message != "" {
		fmt.Fprintf(b, "  %s\n", wrap(exc.Message, "  ", width-2))
	}
	// The log line that produced the event often carries context the exception
	// message does not ("gallery: claim pending asset failing repeatedly" vs.
	// "connection refused"), so show it whenever it says something different.
	if ev.Message != "" && ev.Message != exc.Message {
		fmt.Fprintf(b, "  %s %s\n", labelStyle.Render(loggedAsLabel),
			wrap(ev.Message, "  ", width-len(loggedAsLabel)-3))
	}
}

func writeStackSection(b *strings.Builder, exc event.Exception) {
	if len(exc.Stacktrace) == 0 {
		return
	}
	writeHeading(b, fmt.Sprintf("Stack Trace (%d frames)", len(exc.Stacktrace)))
	for _, f := range exc.Stacktrace {
		writeFrame(b, f)
	}
}

// writeFrame renders one stack frame: the resolved function and location on one
// line, with the minified origin and source snippet beneath it when the frame
// was symbolicated from a source map.
func writeFrame(b *strings.Builder, f event.StackFrame) {
	fn, file, line := frameLocation(f)
	loc := file
	if line > 0 {
		loc = fmt.Sprintf("%s:%d", file, line)
	}
	// The Go SDK fills Module with the frame's source file name, which the
	// location already spells out — only show it when it adds something.
	if f.Module != "" && f.Module != path.Base(file) {
		loc += " [" + f.Module + "]"
	}
	fmt.Fprintf(b, "  %s %s\n", frameStyle.Render(fn), timeStyle.Render(loc))
	if f.OriginalFile != "" && f.File != "" && f.File != f.OriginalFile {
		fmt.Fprintf(b, "      %s\n", dimStyle.Render(fmt.Sprintf("built from %s:%d:%d", f.File, f.Line, f.Column)))
	}
	if f.Snippet != "" {
		fmt.Fprintf(b, "      %s\n", dimStyle.Render(strings.TrimSpace(f.Snippet)))
	}
}

func writeUserSection(b *strings.Builder, u event.UserContext) {
	if u.ID == "" && u.Email == "" && u.Username == "" {
		return
	}
	writeHeading(b, "User")
	writeField(b, "ID", u.ID)
	writeField(b, "Username", u.Username)
	writeField(b, "Email", u.Email)
}

func writeBreadcrumbsSection(b *strings.Builder, crumbs []event.Breadcrumb, width int) {
	if len(crumbs) == 0 {
		return
	}
	writeHeading(b, fmt.Sprintf("Breadcrumbs (%d)", len(crumbs)))
	shown := crumbs
	if len(shown) > maxBreadcrumbs {
		fmt.Fprintf(b, "  %s\n", dimStyle.Render(fmt.Sprintf("… %d earlier breadcrumbs omitted", len(shown)-maxBreadcrumbs)))
		shown = shown[len(shown)-maxBreadcrumbs:]
	}
	for _, c := range shown {
		writeBreadcrumb(b, c, width)
	}
}

func writeBreadcrumb(b *strings.Builder, c event.Breadcrumb, width int) {
	level := c.Level
	if level == "" {
		level = "info"
	}
	fmt.Fprintf(b, "  %s %s %s %s\n",
		timeStyle.Render(clockTime(c.Timestamp)),
		severityStyle(level).Render(fmt.Sprintf("%-5s", level)),
		projectStyle.Render(c.Category),
		wrap(truncate(c.Message, maxValueWidth), "      ", width-breadcrumbPrefixWidth-len(c.Category)))
	writeSortedMap(b, c.Data, "        ", width)
}

func writeMapSection(b *strings.Builder, title string, values map[string]any, width int) {
	if len(values) == 0 {
		return
	}
	writeHeading(b, title)
	writeSortedMap(b, values, "  ", width)
}
