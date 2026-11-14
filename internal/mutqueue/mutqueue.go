// Package mutqueue provides a durable NDJSON queue for admin mutations
// (resolve, reopen, mute, unmute). It decouples HTTP handlers from SQLite
// writes: handlers append a record and return 202 immediately; the background
// worker drains and applies the records at its own pace.
package mutqueue

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	activeFile     = "mutations.ndjson"
	processingFile = "mutations.processing.ndjson"
	deadLetterFile = "mutations.deadletter.ndjson"
)

// Op is the mutation operation type.
type Op string

const (
	OpResolve Op = "resolve"
	OpReopen  Op = "reopen"
	OpMute    Op = "mute"
	OpUnmute  Op = "unmute"
)

// Record is a single queued mutation.
type Record struct {
	IssueID  string    `json:"issue_id"`
	Op       Op        `json:"op"`
	MuteMode string    `json:"mute_mode,omitempty"`
	QueuedAt time.Time `json:"queued_at"`
}

// Queue is a durable append-only queue stored as NDJSON on disk.
// Use New to open one; call Drain on the same instance so file rotation
// is coordinated under the internal mutex.
type Queue struct {
	mu   sync.Mutex
	dir  string
	path string
	file *os.File
}

// New opens (or creates) a mutation queue in dir.
func New(dir string) (*Queue, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, activeFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Queue{dir: dir, path: path, file: f}, nil
}

// Append durably appends a mutation record to the queue.
func (q *Queue) Append(r Record) error {
	if r.QueuedAt.IsZero() {
		r.QueuedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("mutqueue: marshal: %w", err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, err := q.file.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("mutqueue: write: %w", err)
	}
	return q.file.Sync()
}

// Drain atomically claims all pending records and calls apply for each one.
// File rotation (active → processing) is done under the lock so in-flight
// Append calls always land on the correct file. Returns nil when the queue
// was empty or all records were applied successfully. On apply failure the
// processing file is left on disk and retried on the next Drain call.
func (q *Queue) Drain(apply func(Record) error) error {
	// Recover any leftover processing file from a previous crashed drain first.
	procPath := filepath.Join(q.dir, processingFile)
	if err := drainFile(procPath, apply); err != nil {
		return err
	}

	// Under the lock: rename active → processing and open a fresh active file.
	// Appends during this window are queued behind the mutex.
	q.mu.Lock()
	renameErr := os.Rename(q.path, procPath)
	if renameErr == nil {
		// Open a new active file before releasing the lock so appenders never see
		// a missing file.
		newFile, openErr := os.OpenFile(q.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr != nil {
			q.mu.Unlock()
			return fmt.Errorf("mutqueue: reopen: %w", openErr)
		}
		oldFile := q.file
		q.file = newFile
		q.mu.Unlock()
		_ = oldFile.Close()
	} else {
		q.mu.Unlock()
		if errors.Is(renameErr, os.ErrNotExist) {
			return nil // nothing queued
		}
		return fmt.Errorf("mutqueue: claim: %w", renameErr)
	}

	return drainFile(procPath, apply)
}

// Close closes the underlying file.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.file == nil {
		return nil
	}
	return q.file.Close()
}

// drainFile applies the records in path one at a time and deletes the file when
// it is done. After each record is handled the byte offset after it is written
// to a cursor file, so a failed or interrupted drain resumes after the last
// applied record and never applies one twice. A line that does not parse is
// appended to the dead-letter file with an ERROR log and skipped, so one bad
// line cannot block the queue.
func drainFile(path string, apply func(Record) error) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(cursorPath(path))
			return nil
		}
		return fmt.Errorf("mutqueue: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	offset, err := readCursor(path)
	if err != nil {
		return err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("mutqueue: seek %s: %w", path, err)
	}

	if err := applyLines(path, bufio.NewReader(f), offset, apply); err != nil {
		return err
	}
	// Remove the file before its cursor: a crash in between leaves a cursor with
	// no file, which the next drain clears, never a file whose cursor is lost.
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = os.Remove(cursorPath(path))
	return nil
}

// applyLines handles each line from r in order, committing the cursor after a
// complete line. offset is the byte position r starts at.
func applyLines(path string, r *bufio.Reader, offset int64, apply func(Record) error) error {
	for {
		line, readErr := r.ReadBytes('\n')
		offset += int64(len(line))
		if err := applyLine(path, line, offset, apply); err != nil {
			return err
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		return fmt.Errorf("mutqueue: read %s: %w", path, readErr)
	}
}

// applyLine handles one line read with its newline. A final line without a
// newline is a torn write: it is applied or dead-lettered like any other, not
// retried forever, and gets no cursor because the file is finished.
func applyLine(path string, line []byte, endOffset int64, apply func(Record) error) error {
	if len(line) == 0 {
		return nil
	}
	if err := handleLine(path, bytes.TrimSuffix(line, []byte("\n")), apply); err != nil {
		return err
	}
	if line[len(line)-1] != '\n' {
		return nil
	}
	return writeCursor(path, endOffset)
}

// handleLine applies one line, or dead-letters it when it does not parse. Only
// an apply failure is returned, because that is the retryable one.
func handleLine(path string, line []byte, apply func(Record) error) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	var r Record
	if err := json.Unmarshal(line, &r); err != nil {
		slog.Error("mutqueue: dead-lettering unparseable mutation", "error", err, "line", string(line))
		return appendDeadLetter(filepath.Dir(path), line)
	}
	if err := apply(r); err != nil {
		return fmt.Errorf("mutqueue: apply %s %s: %w", r.Op, r.IssueID, err)
	}
	return nil
}

func appendDeadLetter(dir string, line []byte) error {
	f, err := os.OpenFile(filepath.Join(dir, deadLetterFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("mutqueue: open dead letter: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(append([]byte(nil), line...), '\n')); err != nil {
		return fmt.Errorf("mutqueue: write dead letter: %w", err)
	}
	return f.Sync()
}

func cursorPath(path string) string { return path + ".cursor" }

func readCursor(path string) (int64, error) {
	data, err := os.ReadFile(cursorPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("mutqueue: read cursor: %w", err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || n < 0 {
		return 0, nil // an unreadable cursor restarts the file; applying is idempotent per op
	}
	return n, nil
}

func writeCursor(path string, offset int64) error {
	tmp := cursorPath(path) + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(offset, 10)), 0o600); err != nil {
		return fmt.Errorf("mutqueue: write cursor: %w", err)
	}
	if err := os.Rename(tmp, cursorPath(path)); err != nil {
		return fmt.Errorf("mutqueue: commit cursor: %w", err)
	}
	return nil
}
