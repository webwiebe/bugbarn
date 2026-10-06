package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Segment lifecycle
//
// The ingest handler appends to the active segment (ingest.ndjson). Once it
// grows past the rotation threshold the worker renames it to a rotated segment
// (ingest-<UTC timestamp>.ndjson) and a fresh active segment takes the new
// appends. The worker's Position names the segment it is reading: while a
// rotated segment still holds unprocessed records the worker drains it first,
// then commits a position on the active segment and deletes the rotated one.
//
// Crash safety rests on two orderings:
//
//   - RotateIfExceedsAt durably commits a position that names the new segment
//     BEFORE renaming the active file. A crash after the commit and before the
//     rename leaves a position naming a segment that does not exist, which
//     means "the rename never happened": resume the active segment.
//   - The worker durably commits a position on the active segment BEFORE it
//     deletes a drained rotated segment. A rotated segment the position does
//     not name has therefore been fully processed and is safe to delete.

const (
	segmentPrefix = "ingest-"
	segmentSuffix = ".ndjson"
	cursorTmpName = cursorFileName + ".tmp"
)

// Position is the worker's read position in the spool: a byte offset into the
// active segment (Segment == "") or into a rotated segment that still holds
// unprocessed records (Segment is that segment's file name).
type Position struct {
	Segment string `json:"segment,omitempty"`
	Offset  int64  `json:"offset"`
}

// SegmentPath returns the file the position's segment lives in.
func SegmentPath(dir, segment string) string {
	if segment == "" {
		return Path(dir)
	}
	if dir == "" {
		dir = ".data/spool"
	}
	return filepath.Join(dir, segment)
}

// isSegmentName reports whether name is a rotated segment file name. The
// active segment, cursor, dead-letter and mutation files never match.
func isSegmentName(name string) bool {
	return strings.HasPrefix(name, segmentPrefix) &&
		strings.HasSuffix(name, segmentSuffix) &&
		len(name) > len(segmentPrefix)+len(segmentSuffix) &&
		!strings.ContainsAny(name, `/\`)
}

// ListSegments returns the rotated segment file names in dir, oldest first.
func ListSegments(dir string) ([]string, error) {
	if dir == "" {
		dir = ".data/spool"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list spool segments: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && isSegmentName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// SegmentExists reports whether the rotated segment is present in dir.
func SegmentExists(dir, segment string) (bool, error) {
	_, err := os.Stat(SegmentPath(dir, segment))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat spool segment %s: %w", segment, err)
}

// RemoveSegment deletes a rotated segment and returns its size in bytes. It
// refuses any name that is not a rotated segment, so it can never remove the
// active segment, the cursor or the dead-letter file.
func RemoveSegment(dir, segment string) (int64, error) {
	if !isSegmentName(segment) {
		return 0, fmt.Errorf("remove spool segment: %q is not a rotated segment", segment)
	}
	path := SegmentPath(dir, segment)
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat spool segment %s: %w", segment, err)
	}
	if err := os.Remove(path); err != nil {
		return 0, fmt.Errorf("remove spool segment %s: %w", segment, err)
	}
	return info.Size(), nil
}

// ReadPosition reads the persisted position from cursor.json in dir. A cursor
// written before segments were tracked has no segment and reads as a position
// on the active segment. A missing cursor reads as the zero position.
func ReadPosition(dir string) (Position, error) {
	if dir == "" {
		dir = ".data/spool"
	}
	data, err := os.ReadFile(filepath.Join(dir, cursorFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Position{}, nil
		}
		return Position{}, err
	}
	var c cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return Position{}, err
	}
	return Position(c), nil
}

// WritePosition persists the position with an atomic replace of cursor.json.
// It does not fsync: losing the latest per-record advance in a crash only
// re-processes those records. Segment transitions use CommitPosition.
func WritePosition(dir string, pos Position) error {
	return writePosition(dir, pos, false)
}

// CommitPosition persists the position and fsyncs both the cursor file and the
// spool directory, so the position survives a crash before any file operation
// that depends on it (a rotation rename or a segment delete).
func CommitPosition(dir string, pos Position) error {
	return writePosition(dir, pos, true)
}

func writePosition(dir string, pos Position, durable bool) error {
	if dir == "" {
		dir = ".data/spool"
	}
	data, err := json.Marshal(cursor(pos))
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, cursorTmpName)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write cursor: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write cursor: %w", err)
	}
	if durable {
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return fmt.Errorf("sync cursor: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close cursor: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, cursorFileName)); err != nil {
		return fmt.Errorf("replace cursor: %w", err)
	}
	if durable {
		return syncDir(dir)
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("sync spool dir: %w", err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return fmt.Errorf("sync spool dir: %w", err)
	}
	return d.Close()
}

// RotateIfExceedsAt rotates the active segment once it exceeds maxBytes and
// returns the rotated segment's name ("" when no rotation happened). offset is
// the caller's read position in the active segment: before renaming, it is
// durably committed as a position in the new segment, so the reader resumes
// exactly where it was and no record appended before the rotation is skipped.
// The stat, commit and rename+reopen run under the spool lock, so no append
// lands on the old file after it has been renamed.
func (s *Spool) RotateIfExceedsAt(maxBytes, offset int64) (string, error) {
	if s == nil {
		return "", errors.New("spool is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := s.file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() <= maxBytes {
		return "", nil
	}
	name, err := s.newSegmentName()
	if err != nil {
		return "", err
	}
	if err := CommitPosition(s.dir, Position{Segment: name, Offset: offset}); err != nil {
		return "", fmt.Errorf("spool rotate commit cursor: %w", err)
	}
	if err := s.rotateLockedTo(name); err != nil {
		return "", err
	}
	return name, nil
}

// newSegmentName returns an unused rotated segment name. Timestamps carry
// nanoseconds, but some clocks only tick in microseconds, so a collision gets a
// numeric suffix instead of silently replacing an existing segment.
func (s *Spool) newSegmentName() (string, error) {
	ts := time.Now().UTC().Format("20060102T150405.000000000Z")
	for i := 0; i < 1000; i++ {
		name := segmentPrefix + ts + segmentSuffix
		if i > 0 {
			name = fmt.Sprintf("%s%s-%03d%s", segmentPrefix, ts, i, segmentSuffix)
		}
		exists, err := SegmentExists(s.dir, name)
		if err != nil {
			return "", err
		}
		if !exists {
			return name, nil
		}
	}
	return "", errors.New("spool rotate: no free segment name")
}

// rotateLockedTo renames the active segment to name and opens a fresh active
// segment; caller must hold s.mu.
func (s *Spool) rotateLockedTo(name string) error {
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("spool rotate close: %w", err)
	}
	if err := os.Rename(s.path, filepath.Join(s.dir, name)); err != nil {
		// Reopen the active segment so appends keep working after a failed
		// rotation; the next tick retries it.
		if file, openErr := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); openErr == nil {
			s.file = file
		}
		return fmt.Errorf("spool rotate rename: %w", err)
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("spool rotate open: %w", err)
	}
	s.file = file
	return nil
}
