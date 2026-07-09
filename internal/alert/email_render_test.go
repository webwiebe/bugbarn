package alert

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestIssueURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, public, id, want string
	}{
		{"empty base yields no link", "", "GEO-107", ""},
		{"whitespace base yields no link", "   ", "GEO-107", ""},
		{"absolute base", "https://bugbarn.wiebe.xyz", "GEO-107", "https://bugbarn.wiebe.xyz/app/#/issues/GEO-107"},
		{"trailing slash trimmed", "https://bugbarn.wiebe.xyz/", "GEO-107", "https://bugbarn.wiebe.xyz/app/#/issues/GEO-107"},
		{"bare host upgraded to https", "bugbarn.wiebe.xyz", "GEO-107", "https://bugbarn.wiebe.xyz/app/#/issues/GEO-107"},
	}
	for _, c := range cases {
		if got := issueURL(c.public, c.id); got != c.want {
			t.Errorf("%s: issueURL(%q,%q)=%q, want %q", c.name, c.public, c.id, got, c.want)
		}
	}
}

// TestHTMLTmpl_NoRelativeLink guards the x-webdoc bug: with no public URL the
// email must not contain any href (which would otherwise be a relative path
// that mail clients resolve to x-webdoc://).
func TestHTMLTmpl_NoRelativeLink(t *testing.T) {
	t.Parallel()
	data := alertMailData{AlertName: "Admin notifications", Title: "boom", Severity: "error"}
	var buf bytes.Buffer
	if err := alertHTMLTmpl.Execute(&buf, data.escaped()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(buf.String(), "href=") {
		t.Errorf("expected no href when IssueURL is empty, got:\n%s", buf.String())
	}
}

func TestHTMLTmpl_RegressionSparkline(t *testing.T) {
	t.Parallel()
	data := alertMailData{
		AlertName:  "Admin notifications",
		Title:      "boom",
		Severity:   "error",
		IssueURL:   "https://bugbarn.wiebe.xyz/app/#/issues/GEO-107",
		Regression: true,
		Sparkline:  buildSparkline([24]int{0, 1, 3, 0, 5, 2}),
	}
	var buf bytes.Buffer
	if err := alertHTMLTmpl.Execute(&buf, data.escaped()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Events over the last 24 hours") {
		t.Error("expected sparkline heading")
	}
	if strings.Count(out, "width:7px") != 24 {
		t.Errorf("expected 24 sparkline bars, got %d", strings.Count(out, "width:7px"))
	}
}

func TestHTMLTmpl_EscapesUntrustedFields(t *testing.T) {
	t.Parallel()
	data := alertMailData{
		AlertName: "Admin notifications",
		Title:     `<script>alert(1)</script>`,
		Message:   `<img src=x onerror=alert(2)>`,
		// Severity is reporter-controlled (from the ingested event's level) and
		// Condition can carry a raw rule string; both must be HTML-escaped.
		Severity:  `<img src=x onerror=alert(3)>`,
		Condition: `<svg onload=alert(4)>`,
	}
	var buf bytes.Buffer
	if err := alertHTMLTmpl.Execute(&buf, data.escaped()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "<script>") || strings.Contains(out, "<img") || strings.Contains(out, "<svg") {
		t.Errorf("untrusted fields not escaped:\n%s", out)
	}
	// The escaped, harmless representation must still be present.
	if !strings.Contains(out, "&lt;img src=x onerror=alert(3)&gt;") {
		t.Errorf("expected escaped severity in output:\n%s", out)
	}
}

func TestBugbarnTag(t *testing.T) {
	t.Parallel()
	if got := bugbarnTag(""); got != "[BugBarn]" {
		t.Errorf("empty env: got %q", got)
	}
	if got := bugbarnTag("  "); got != "[BugBarn]" {
		t.Errorf("blank env: got %q", got)
	}
	if got := bugbarnTag("staging"); got != "[BugBarn · staging]" {
		t.Errorf("staging: got %q", got)
	}
}

func TestHTMLTmpl_ShowsOrigin(t *testing.T) {
	t.Parallel()
	data := alertMailData{AlertName: "Admin notifications", Origin: "staging", Title: "boom", Severity: "error"}
	var buf bytes.Buffer
	if err := alertHTMLTmpl.Execute(&buf, data.escaped()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[BugBarn · staging]") {
		t.Errorf("expected env-labeled header, got:\n%s", out)
	}
	// No origin -> plain tag, no badge.
	var buf2 bytes.Buffer
	_ = alertHTMLTmpl.Execute(&buf2, (alertMailData{AlertName: "x", Title: "y"}).escaped())
	if !strings.Contains(buf2.String(), "[BugBarn]") || strings.Contains(buf2.String(), "·") {
		t.Errorf("expected plain [BugBarn] when origin unset")
	}
}

func TestBuildSparkline(t *testing.T) {
	t.Parallel()
	bars := buildSparkline([24]int{})
	if len(bars) != 24 {
		t.Fatalf("want 24 bars, got %d", len(bars))
	}
	for _, b := range bars {
		if !b.Zero || b.Height != 2 {
			t.Fatalf("all-zero input should give faint 2px bars, got %+v", b)
		}
	}
	var counts [24]int
	counts[10] = 10
	counts[11] = 5
	bars = buildSparkline(counts)
	if bars[10].Height != 40 { // max scales to 40px
		t.Errorf("peak bar height = %d, want 40", bars[10].Height)
	}
	if bars[11].Height <= 2 || bars[11].Height >= 40 {
		t.Errorf("mid bar height = %d, want between 2 and 40", bars[11].Height)
	}
}

func TestLocationAndMessage(t *testing.T) {
	t.Parallel()
	ev := event.Event{
		Message: "fallback message",
		Exception: event.Exception{
			Message: "cannot read property 'x' of undefined",
			Stacktrace: []event.StackFrame{
				{Function: "handleClick", File: "app/ui/button.ts", Line: 42},
			},
		},
		Attributes: map[string]any{"environment": "production"},
		Resource:   map[string]any{"service.version": "v1.4.2"},
	}
	if got := messageOf(ev); got != "cannot read property 'x' of undefined" {
		t.Errorf("messageOf = %q", got)
	}
	if got := locationOf(ev); got != "handleClick (app/ui/button.ts:42)" {
		t.Errorf("locationOf = %q", got)
	}
	if got := firstAttr(ev, "environment"); got != "production" {
		t.Errorf("firstAttr(environment) = %q", got)
	}
	if got := firstAttr(ev, "release", "service.version"); got != "v1.4.2" {
		t.Errorf("firstAttr(release) = %q", got)
	}
}

// TestGenerateEmailPreview renders sample new-issue and regression emails to
// files when EMAIL_PREVIEW_DIR is set. It is a no-op in normal CI runs.
func TestGenerateEmailPreview(t *testing.T) {
	dir := os.Getenv("EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set EMAIL_PREVIEW_DIR to render email previews")
	}
	ev := event.Event{
		Severity: "error",
		Exception: event.Exception{
			Message: "expect(locator).toBeVisible() failed: element not found",
			Stacktrace: []event.StackFrame{
				{Function: "Object.<anonymous>", File: "e2e/full-app-discovery-analytics.spec.ts", Line: 218},
			},
		},
		Attributes: map[string]any{"environment": "production"},
		Resource:   map[string]any{"service.version": "v0.236.140"},
	}
	base := alertMailData{
		AlertName:   "Admin notifications",
		Origin:      "staging",
		Title:       "E2EFailure: e2e: full-app-discovery-analytics.spec.ts › Discovery Page › can remove a competitor before applying [webkit]",
		IssueURL:    "https://bugbarn.wiebe.xyz/app/#/issues/GEO-107",
		Severity:    "error",
		Project:     "geo",
		Message:     messageOf(ev),
		Location:    locationOf(ev),
		Environment: firstAttr(ev, "environment"),
		Release:     firstAttr(ev, "release", "service.version"),
		EventCount:  7,
		FirstSeen:   time.Now().Add(-6 * time.Hour).UTC().Format("2006-01-02 15:04 UTC"),
		LastSeen:    time.Now().UTC().Format("2006-01-02 15:04 UTC"),
	}

	newIssue := base
	newIssue.Condition = conditionLabel("new_issue")

	regression := base
	regression.Condition = conditionLabel("regression")
	regression.Regression = true
	regression.RegressionCount = 3
	regression.Sparkline = buildSparkline([24]int{0, 0, 1, 0, 2, 1, 0, 0, 3, 5, 2, 1, 0, 0, 0, 4, 6, 2, 1, 0, 0, 2, 5, 7})

	for name, d := range map[string]alertMailData{"new-issue": newIssue, "regression": regression} {
		var buf bytes.Buffer
		if err := alertHTMLTmpl.Execute(&buf, d.escaped()); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		path := filepath.Join(dir, "alert-"+name+".html")
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		t.Logf("wrote %s", path)
	}
}
