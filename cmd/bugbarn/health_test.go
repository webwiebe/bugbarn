package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func TestSpoolBacklogSourcePicksTheWriterSpool(t *testing.T) {
	writerSpool := t.TempDir()
	if err := os.WriteFile(spool.Path(writerSpool), make([]byte, 500), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := spool.WritePosition(writerSpool, spool.Position{Offset: 200}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)

	cases := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{"writer reads its own spool", config.Config{Mode: "writer", SpoolDir: writerSpool}, true},
		{"monolith reads its own spool", config.Config{SpoolDir: writerSpool}, true},
		{"reader reads the writer spool", config.Config{Mode: "reader", SpoolDir: t.TempDir(), WriterSpoolDir: writerSpool}, true},
		{"reader without the writer spool skips the check", config.Config{Mode: "reader", SpoolDir: writerSpool}, false},
		{"unreadable path skips the check", config.Config{Mode: "reader", WriterSpoolDir: filepath.Join(writerSpool, "missing")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := spoolBacklogSource(tc.cfg, logger)
			if (src != nil) != tc.want {
				t.Fatalf("source wired = %v, want %v", src != nil, tc.want)
			}
			if src == nil {
				return
			}
			b, err := src(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if b.Bytes != 300 || b.LastAdvanceAt.IsZero() {
				t.Fatalf("unexpected backlog: %+v", b)
			}
		})
	}
}
