package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestApplyConfigFile pins how a KEY=VALUE config file reaches the
// environment: comments, blanks and lines without a key are skipped, one pair
// of matching quotes is stripped, and a variable that is already set wins.
func TestApplyConfigFile(t *testing.T) {
	const file = `# a comment
   # an indented comment

BBT_PLAIN=value
  BBT_SPACED  =  spaced value
BBT_DQ="double"
BBT_SQ='single'
BBT_MIXED="mixed'
BBT_ONEQUOTE="
BBT_EMPTYQ=""
BBT_EQ=a=b
NOEQUALS
=novalue
BBT_SET=fromfile
BBT_DUP=first
BBT_DUP=second
BBT_EMPTYENV=fromfile
`
	want := map[string]string{
		"BBT_PLAIN":    "value",
		"BBT_SPACED":   "spaced value",
		"BBT_DQ":       "double",
		"BBT_SQ":       "single",
		"BBT_MIXED":    `"mixed'`,
		"BBT_ONEQUOTE": `"`,
		"BBT_EMPTYQ":   "",
		"BBT_EQ":       "a=b",
		"BBT_SET":      "fromenv",
		"BBT_DUP":      "first",
		"BBT_EMPTYENV": "fromfile",
		"NOEQUALS":     "",
	}
	// t.Setenv restores every key after the test. An empty value counts as
	// unset, so the file may fill it.
	for key := range want {
		t.Setenv(key, "")
	}
	t.Setenv("BBT_SET", "fromenv")

	path := filepath.Join(t.TempDir(), "bugbarn.conf")
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyConfigFile(path); err != nil {
		t.Fatalf("applyConfigFile: %v", err)
	}
	for key, val := range want {
		if got := os.Getenv(key); got != val {
			t.Errorf("%s = %q, want %q", key, got, val)
		}
	}
}

func TestApplyConfigFileMissing(t *testing.T) {
	err := applyConfigFile(filepath.Join(t.TempDir(), "absent.conf"))
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want a not-exist error", err)
	}
}
