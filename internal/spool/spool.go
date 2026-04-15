package spool

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const DefaultFileName = "ingest.ndjson"

type Record struct {
	IngestID      string    `json:"ingestId"`
	ReceivedAt    time.Time `json:"receivedAt"`
	ContentType   string    `json:"contentType,omitempty"`
	RemoteAddr    string    `json:"remoteAddr,omitempty"`
	ContentLength int64     `json:"contentLength,omitempty"`
	BodyBase64    string    `json:"bodyBase64"`
}

func New(dir string) (*Spool, error) {
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
		dir:  dir,
		path: filePath,
		file: file,
	}, nil
}

type Spool struct {
	mu   sync.Mutex
	dir  string
	path string
	file *os.File
}

func (s *Spool) Path() string {
	if s == nil {
		return ""
	}
	return s.path
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

	if _, err := s.file.Write(append(payload, '\n')); err != nil {
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
