package fingerprint

import (
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestNormalizeMessageCollapsesURLPaths(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, in, want string }{
		{"path", "get https://mywed.com/en/france/lyon-wedding-photographers/: 418", "get https://mywed.com/<path> <num>"},
		{"query", "fetch http://api.test:8080/v1/items?id=7&page=2 failed", "fetch http://api.test:<num>/<path> failed"},
		{"fragment", "see https://x.test/docs#intro", "see https://x.test/<path>"},
		{"quoted", `got "https://x.test/a/b" twice`, `got "https://x.test/<path>" twice`},
		{"no path", "dial https://x.test refused", "dial https://x.test refused"},
		{"two urls", "https://a.test/1 then https://b.test/2", "https://a.test/<path> then https://b.test/<path>"},
		{"plain text keeps slashes", "read /var/log/app.log failed", "read /var/log/app.log failed"},
	}
	for _, tc := range cases {
		if got := normalizeMessage(tc.in); got != tc.want {
			t.Errorf("%s: normalizeMessage(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func scrapeError(url, region string) event.Event {
	return event.Event{
		Exception: event.Exception{Type: "*scrape.StatusError", Message: "GET " + url + ": 418 I'm a teapot"},
		Attributes: map[string]any{
			"region":      region,
			"environment": "production",
		},
	}
}

func TestFingerprintGroupsErrorsDifferingOnlyByURLPath(t *testing.T) {
	t.Parallel()

	first := scrapeError("https://mywed.com/en/france/saint-georges-d-orques-wedding-photographers/", "france/saint-georges-d-orques")
	for _, other := range []event.Event{
		scrapeError("https://mywed.com/en/italy/rome-wedding-photographers/", "italy/rome"),
		scrapeError("https://mywed.com/nl/belgie/gent/?page=3", "belgie/gent"),
	} {
		if Fingerprint(first) != Fingerprint(other) {
			t.Errorf("URL path split the issue:\n%s\n%s", Material(first), Material(other))
		}
	}
}

func TestFingerprintKeepsDifferentHostsAndCategoriesApart(t *testing.T) {
	t.Parallel()

	base := scrapeError("https://mywed.com/a/", "eu-west-1")
	if Fingerprint(base) == Fingerprint(scrapeError("https://other.com/a/", "eu-west-1")) {
		t.Error("a different host must stay a different issue")
	}
	prod := scrapeError("https://mywed.com/a/", "eu-west-1")
	staging := scrapeError("https://mywed.com/a/", "eu-west-1")
	staging.Attributes["environment"] = "staging"
	if Fingerprint(prod) == Fingerprint(staging) {
		t.Error("a different environment must stay a different issue")
	}
	other := scrapeError("https://mywed.com/a/", "eu-west-1")
	other.Exception.Message = "GET https://mywed.com/a/: 500 server error"
	if Fingerprint(base) == Fingerprint(other) {
		t.Error("a different failure must stay a different issue")
	}
}

// A client that reports one error per region must not open an issue per region
// (#241): region is not in the context allowlist, in any spelling.
func TestRegionDoesNotSplitIssues(t *testing.T) {
	t.Parallel()

	first := scrapeError("https://mywed.com/a/", "eu-west-1")
	for _, other := range []event.Event{
		scrapeError("https://mywed.com/a/", "us-east-1"),
		scrapeError("https://mywed.com/a/", "france/lyon"),
		scrapeError("https://mywed.com/a/", ""),
	} {
		if Fingerprint(first) != Fingerprint(other) {
			t.Errorf("region split the issue:\n%s\n%s", Material(first), Material(other))
		}
	}
	cloud := scrapeError("https://mywed.com/a/", "eu-west-1")
	cloud.Resource = map[string]any{"cloud": map[string]any{"region": "eu-central-1"}}
	if Fingerprint(first) != Fingerprint(cloud) {
		t.Error("cloud.region split the issue")
	}
	if strings.Contains(strings.Join(Explanation(first), "|"), "context.region") {
		t.Error("the explanation must not list region as fingerprint context")
	}
}

func TestRouteTemplatesKeepTheirSlashes(t *testing.T) {
	t.Parallel()

	a := event.Event{Exception: event.Exception{Message: "boom"}, Attributes: map[string]any{"http.route": "/users/:id"}}
	b := event.Event{Exception: event.Exception{Message: "boom"}, Attributes: map[string]any{"http.route": "/orders/:id"}}
	if Fingerprint(a) == Fingerprint(b) {
		t.Error("distinct route templates must stay distinct")
	}
}

func TestExplanationShowsTheURLPlaceholder(t *testing.T) {
	t.Parallel()

	got := strings.Join(Explanation(scrapeError("https://mywed.com/en/x/", "france/lyon")), "|")
	for _, want := range []string{"exception.message=get https://mywed.com/<path>", "context.environment=production"} {
		if !strings.Contains(got, want) {
			t.Errorf("explanation lacks %q: %s", want, got)
		}
	}
}
