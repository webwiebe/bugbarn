// Package mutqueue provides a durable NDJSON queue for admin mutations
// (resolve, reopen, mute, unmute). It decouples HTTP handlers from SQLite
// writes: handlers append a record and return 202 immediately; the background
// worker drains and applies the records at its own pace.
package mutqueue

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	activeFile     = "mutations.ndjson"
	processingFile = "mutations.processing.ndjson"
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

// drainFile reads records from path, calls apply for each, and deletes the
// file on success. On failure the file is left for the next Drain call.
func drainFile(path string, apply func(Record) error) error {
	records, err := readRecords(path)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		_ = os.Remove(path)
		return nil
	}
	for _, r := range records {
		if err := apply(r); err != nil {
			return fmt.Errorf("mutqueue: apply %s %s: %w", r.Op, r.IssueID, err)
		}
	}
	return os.Remove(path)
}

func readRecords(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("mutqueue: open %s: %w", path, err)
	}
	defer f.Close()

	var records []Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("mutqueue: parse: %w", err)
		}
		records = append(records, r)
	}
	return records, sc.Err()
}
