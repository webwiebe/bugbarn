package sourcemap

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func TestResolveMapsGeneratedToOriginal(t *testing.T) {
	pos := Resolve(fixture(t, "app.js.map"), 1, 9)
	if pos == nil {
		t.Fatal("expected a position")
	}
	if pos.File != "src/app.ts" || pos.Line != 1 || pos.Column != 9 || pos.Function != "greet" {
		t.Fatalf("unexpected position %+v", *pos)
	}
}

func TestResolveReturnsNilForBadInput(t *testing.T) {
	if Resolve([]byte("not json"), 1, 0) != nil {
		t.Fatal("invalid map should resolve to nil")
	}
	if Resolve(fixture(t, "app.js.map"), 99, 0) != nil {
		t.Fatal("unmapped line should resolve to nil")
	}
}

func TestResolveWithSnippetIncludesContext(t *testing.T) {
	pos, snippet := ResolveWithSnippet(fixture(t, "app.js.map"), 2, 0)
	if pos == nil || pos.Line != 2 {
		t.Fatalf("unexpected position %+v", pos)
	}
	want := "line one\nfunction greet() {\n  return 1;"
	if snippet != want {
		t.Fatalf("snippet = %q, want %q", snippet, want)
	}
}

func TestResolveWithSnippetWithoutEmbeddedSource(t *testing.T) {
	pos, snippet := ResolveWithSnippet(fixture(t, "nosource.js.map"), 1, 0)
	if pos == nil || pos.File != "src/app.ts" {
		t.Fatalf("unexpected position %+v", pos)
	}
	if snippet != "" {
		t.Fatalf("snippet = %q, want empty", snippet)
	}
}

func TestResolveWithSnippetFailures(t *testing.T) {
	if pos, snippet := ResolveWithSnippet([]byte("{"), 1, 0); pos != nil || snippet != "" {
		t.Fatal("invalid map should give nothing")
	}
	if pos, snippet := ResolveWithSnippet(fixture(t, "app.js.map"), 50, 0); pos != nil || snippet != "" {
		t.Fatal("unmapped position should give nothing")
	}
}

func TestSnippetFromSourceEdges(t *testing.T) {
	src := "a\nb\nc\nd"
	cases := []struct {
		name string
		src  string
		line int
		want string
	}{
		{"empty source", "", 1, ""},
		{"zero line", src, 0, ""},
		{"negative line", src, -2, ""},
		{"past end", src, 5, ""},
		{"first line clamps start", src, 1, "a\nb"},
		{"middle", src, 2, "a\nb\nc"},
		{"last line clamps end", src, 4, "c\nd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SnippetFromSource(tc.src, tc.line); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
