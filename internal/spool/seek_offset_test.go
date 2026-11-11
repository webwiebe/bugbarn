package spool

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// seekToOffset keeps a valid offset, restarts a stale one past the end of the
// file, and leaves a zero offset at the start.
func TestSeekToOffset(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "spool.ndjson")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		offset     int64
		wantOffset int64
		wantRest   string
	}{
		{"zero stays at start", 0, 0, "0123456789"},
		{"valid offset is kept", 4, 4, "456789"},
		{"offset at end is kept", 10, 10, ""},
		{"offset past end restarts", 11, 0, "0123456789"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, err := seekToOffset(f, tc.offset)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.wantOffset {
				t.Fatalf("offset = %d, want %d", got, tc.wantOffset)
			}
			rest, _ := io.ReadAll(f)
			if string(rest) != tc.wantRest {
				t.Fatalf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}
