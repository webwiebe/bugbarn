package tracing

import (
	"net/http"
	"testing"
)

func TestRouteLabelUsesMatchedPattern(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"GET /api/v1/issues/{id}": "/api/v1/issues/{id}",
		"/api/v1/events":          "/api/v1/events",
		"/":                       "unmatched",
		"":                        "unmatched",
	}
	for pattern, want := range cases {
		if got := routeLabel(&http.Request{Pattern: pattern}); got != want {
			t.Errorf("routeLabel(%q) = %q, want %q", pattern, got, want)
		}
	}
}
