package spool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Backlog is the part of the spool the worker has not processed yet, read from
// the files on disk so a process other than the writer (a reader pod on the
// same volume) can judge whether the worker is making progress.
type Backlog struct {
	// Bytes counts the unprocessed bytes: the rest of the segment the cursor
	// names plus, while a rotated segment drains, the whole active segment.
	Bytes int64
	// LastAdvanceAt is the modification time of cursor.json. The worker
	// rewrites the cursor after every processed record, so this is the last
	// time it made progress. Zero when the worker has never written a cursor.
	LastAdvanceAt time.Time
}

// ReadBacklog reads the worker's cursor and the segment sizes in dir. It never
// writes, so it works on a read-only mount. The cursor and the file sizes are
// read separately, so a sample taken while the worker rotates or finishes a
// segment can be off by up to one segment; a negative result is clamped to 0.
// A missing spool directory is an error so a wrong path does not read as an
// empty spool.
func ReadBacklog(dir string) (Backlog, error) {
	if dir == "" {
		dir = ".data/spool"
	}
	if info, err := os.Stat(dir); err != nil {
		return Backlog{}, fmt.Errorf("spool backlog: %w", err)
	} else if !info.IsDir() {
		return Backlog{}, fmt.Errorf("spool backlog: %s is not a directory", dir)
	}

	pos, err := ReadPosition(dir)
	if err != nil {
		return Backlog{}, fmt.Errorf("spool backlog: read cursor: %w", err)
	}
	var b Backlog
	if info, err := os.Stat(filepath.Join(dir, cursorFileName)); err == nil {
		b.LastAdvanceAt = info.ModTime().UTC()
	}

	active, err := fileSize(Path(dir))
	if err != nil {
		return Backlog{}, err
	}
	if pos.Segment == "" {
		b.Bytes = active - pos.Offset
	} else {
		// A segment that is gone was either never renamed (the offset is in
		// the active segment) or already drained and deleted after the cursor
		// moved on; both read as 0 here.
		segment, err := fileSize(SegmentPath(dir, pos.Segment))
		if err != nil {
			return Backlog{}, err
		}
		b.Bytes = max(segment-pos.Offset, 0) + active
	}
	b.Bytes = max(b.Bytes, 0)
	return b, nil
}

// fileSize returns the size of path, or 0 when it does not exist.
func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info.Size(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return 0, fmt.Errorf("spool backlog: stat %s: %w", filepath.Base(path), err)
}
