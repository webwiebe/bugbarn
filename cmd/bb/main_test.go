package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"testing"
)

// quiet silences stdout and stderr while f runs; runCommand prints usage and errors.
func quiet(t *testing.T, f func()) string {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = w, w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	f()
	w.Close()
	return <-done
}

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no command", []string{"bb"}, 1},
		{"unknown command", []string{"bb", "frobnicate"}, 1},
		{"version", []string{"bb", "version"}, 0},
		{"version flag", []string{"bb", "--version"}, 0},
		{"help", []string{"bb", "help"}, 0},
		{"help flag", []string{"bb", "-h"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got int
			quiet(t, func() { got = runCommand(tc.args) })
			if got != tc.want {
				t.Errorf("runCommand(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestRunMapsCommandErrorsToExitCodes(t *testing.T) {
	old := commands
	defer func() { commands = old }()
	commands = map[string]func([]string) error{
		"ok":   func([]string) error { return nil },
		"help": func([]string) error { return nil }, // shadowed by the built-in help
		"asks": func([]string) error { return flag.ErrHelp },
		"fail": func([]string) error { return errors.New("boom") },
	}

	var out string
	var codes [3]int
	out = quiet(t, func() {
		codes[0] = runCommand([]string{"bb", "ok"})
		codes[1] = runCommand([]string{"bb", "asks"})
		codes[2] = runCommand([]string{"bb", "fail"})
	})
	if codes != [3]int{0, 0, 1} {
		t.Errorf("exit codes = %v, want [0 0 1]", codes)
	}
	if !strings.Contains(out, "error: boom") {
		t.Errorf("the failing command's error was not printed: %q", out)
	}
}

func TestEveryDocumentedCommandIsWired(t *testing.T) {
	usage := quiet(t, printUsage)
	for name := range commands {
		if !strings.Contains(usage, "\n  "+name+" ") {
			t.Errorf("command %q is wired but missing from the usage text", name)
		}
	}
}
