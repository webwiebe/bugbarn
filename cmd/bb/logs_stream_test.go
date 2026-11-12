package main

import (
	"io"
	"os"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestPrintStreamLineFilters(t *testing.T) {
	const entry = `{"project_slug":"a","level_num":30,"level":"info","message":"hi"}`
	cases := []struct {
		name     string
		line     string
		levelMin int
		project  string
		want     string
	}{
		{"not a data line", "event: ping", 0, "", ""},
		{"bad json", "data: {", 0, "", ""},
		{"below level", "data: " + entry, 40, "", ""},
		{"other project", "data: " + entry, 0, "b", ""},
		{"passes raw", "data: " + entry, 30, "a", entry + "\n"},
	}
	for _, c := range cases {
		got := captureStdout(t, func() { printStreamLine(c.line, c.levelMin, c.project, true) })
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
