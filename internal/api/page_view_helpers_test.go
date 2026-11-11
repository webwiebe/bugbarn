package api

import (
	"strings"
	"testing"
)

func TestSplitReferrer(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, host, path string }{
		{"", "", ""},
		{"https://example.com/a/b?x=1", "example.com", "/a/b"},
		{"https://example.com", "example.com", ""},
		{"://bad", "", ""},
	}
	for _, c := range cases {
		host, path := splitReferrer(c.in)
		if host != c.host || path != c.path {
			t.Errorf("splitReferrer(%q) = %q, %q; want %q, %q", c.in, host, path, c.host, c.path)
		}
	}
}

func TestCapPageViewProps(t *testing.T) {
	t.Parallel()

	in := map[string]string{"long": strings.Repeat("x", 500)}
	for i := 0; i < 20; i++ {
		in[string(rune('a'+i))] = "v"
	}
	got := capPageViewProps(in)
	if len(got) != 10 {
		t.Fatalf("kept %d props, want 10", len(got))
	}
	if v, ok := got["long"]; ok && len(v) != 200 {
		t.Fatalf("long value kept at %d bytes, want 200", len(v))
	}
	if got := capPageViewProps(nil); got == nil || len(got) != 0 {
		t.Fatalf("capPageViewProps(nil) = %v, want an empty map", got)
	}
}
