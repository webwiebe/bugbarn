package spool

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const DefaultFileName = "ingest.ndjson"

const cursorFileName = "cursor.json"
const deadLetterFileName = "deadletter.ndjson"

var ErrFull = errors.New("spool is full")

type Record struct {
	IngestID   string    `json:"ingestId"`
	ReceivedAt time.Time `json:"receivedAt"`
	// Kind discriminates the payload. An empty value means an ingest event
	// (the historical default, so already-spooled records keep working);
	// "release" means a release marker created via POST /api/v1/releases.
	Kind          string `json:"kind,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	RemoteAddr    string `json:"remoteAddr,omitempty"`
	ContentLength int64  `json:"contentLength,omitempty"`
	BodyBase64    string `json:"bodyBase64"`
	ProjectSlug   string `json:"projectSlug,omitempty"`
	// ProjectID is the resolved project for non-event records (e.g. releases),
	// captured at enqueue time so the worker need not re-resolve it.
	ProjectID int64 `json:"projectId,omitempty"`
	// Internal marks a record built in-process by BugBarn itself (detections).
	// It keeps IP addresses in the event, which the scrubber otherwise
	// rewrites. Never serialized: a record read from the spool, the write
	// queue or a request can never carry it.
	Internal bool `json:"-"`
}

// cursor is the on-disk form of Position (see segments.go): the byte offset
// just past the last handled record, in the active segment or in the rotated
// segment it names.
type cursor struct {
	Segment string `json:"segment,omitempty"`
	Offset  int64  `json:"offset"`
}

func New(dir string) (*Spool, error) {
	return NewWithLimit(dir, 0)
}

func NewWithLimit(dir string, maxBytes int64) (*Spool, error) {
	if dir == "" {
		dir = ".data/spool"
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	filePath := filepath.Join(dir, DefaultFileName)
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}

	return &Spool{
		dir:      dir,
		path:     filePath,
		file:     file,
		maxBytes: maxBytes,
	}, nil
}

type Spool struct {
	mu       sync.Mutex
	dir      string
	path     string
	file     *os.File
	maxBytes int64
}

func (s *Spool) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func Path(dir string) string {
	if dir == "" {
		dir = ".data/spool"
	}
	return filepath.Join(dir, DefaultFileName)
}

func (s *Spool) Append(record Record) error {
	if s == nil {
		return errors.New("spool is nil")
	}

	if record.BodyBase64 == "" {
		record.BodyBase64 = base64.StdEncoding.EncodeToString(nil)
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.maxBytes > 0 {
		info, err := s.file.Stat()
		if err != nil {
			return err
		}
		if info.Size()+int64(len(payload))+1 > s.maxBytes {
			return ErrFull
		}
	}

	if _, err := s.file.Write(append(payload, '\n')); err != nil {
		return err
	}

	return s.file.Sync()
}

// AppendBatch writes multiple records in a single locked section and calls
// Sync once for the whole batch. This is substantially faster than calling
// Append per-record when throughput matters.
func (s *Spool) AppendBatch(records []Record) error {
	if s == nil {
		return errors.New("spool is nil")
	}
	if len(records) == 0 {
		return nil
	}

	var buf []byte
	for i := range records {
		if records[i].BodyBase64 == "" {
			records[i].BodyBase64 = base64.StdEncoding.EncodeToString(nil)
		}
		payload, err := json.Marshal(records[i])
		if err != nil {
			return err
		}
		buf = append(buf, payload...)
		buf = append(buf, '\n')
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.maxBytes > 0 {
		info, err := s.file.Stat()
		if err != nil {
			return err
		}
		if info.Size()+int64(len(buf)) > s.maxBytes {
			return ErrFull
		}
	}

	if _, err := s.file.Write(buf); err != nil {
		return err
	}
	return s.file.Sync()
}

func (s *Spool) Close() error {
	if s == nil || s.file == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.file.Close()
}

// ReadCursor reads the persisted byte offset from cursor.json in dir.
// Returns 0 if the file does not exist.
func ReadCursor(dir string) (int64, error) {
	if dir == "" {
		dir = ".data/spool"
	}
	path := filepath.Join(dir, cursorFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var c cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return 0, err
	}
	return c.Offset, nil
}

// WriteCursor persists the byte offset on the active segment to cursor.json in
// dir. It is WritePosition for a position without a rotated segment.
func WriteCursor(dir string, offset int64) error {
	return WritePosition(dir, Position{Offset: offset})
}

// ResetCursor removes the cursor file, causing the next startup to reprocess from the beginning.
func ResetCursor(dir string) error {
	if dir == "" {
		dir = ".data/spool"
	}
	path := filepath.Join(dir, cursorFileName)
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// RecordAtOffset pairs a Record with the byte offset *after* this record
// (i.e. the cursor value to persist once the record is handled).
type RecordAtOffset struct {
	Record    Record
	EndOffset int64
}

// ReadRecordsFrom reads records from path starting at the given byte offset.
// It returns each record paired with the file offset immediately after it so
// callers can advance the cursor record-by-record.
func ReadRecordsFrom(path string, offset int64) ([]RecordAtOffset, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	if offset > 0 {
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if offset > info.Size() {
			offset = 0
		}
		if offset > 0 {
			if _, err := file.Seek(offset, 0); err != nil {
				return nil, err
			}
		}
	}

	var records []RecordAtOffset
	pos := offset
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		// Account for the newline that bufio.Scanner strips.
		pos += int64(len(line)) + 1
		if len(line) == 0 {
			continue
		}

		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			// corrupt line (e.g. truncated write during pod restart) — skip it
			slog.Warn("spool: skipping corrupt record", "offset", pos, "error", err)
			continue
		}
		records = append(records, RecordAtOffset{Record: record, EndOffset: pos})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// AppendDeadLetter writes a record to the dead-letter file in dir.
func AppendDeadLetter(dir string, record Record) error {
	if dir == "" {
		dir = ".data/spool"
	}
	path := filepath.Join(dir, deadLetterFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = file.Write(append(payload, '\n'))
	return err
}

func ReadRecords(path string) ([]Record, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var records []Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
