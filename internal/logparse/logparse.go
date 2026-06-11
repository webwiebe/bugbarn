// Package logparse turns a pino-style log ingest body into domain.LogEntry
// values. It is shared by the HTTP log endpoint (api) and the Redis write-queue
// consumer (ingestproc) so both parse logs identically (spec 007).
package logparse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

var levelNames = map[int]string{
	10: "trace",
	20: "debug",
	30: "info",
	40: "warn",
	50: "error",
	60: "fatal",
}

var levelNums = map[string]int{
	"trace": 10,
	"debug": 20,
	"info":  30,
	"warn":  40,
	"error": 50,
	"fatal": 60,
}

var skipFields = map[string]bool{
	"v":        true,
	"pid":      true,
	"hostname": true,
	"msg":      true,
	"message":  true,
	"level":    true,
	"time":     true,
}

// ParseObject converts a single decoded pino log object into a LogEntry.
func ParseObject(obj map[string]any, projectID int64) domain.LogEntry {
	entry := domain.LogEntry{
		ProjectID:  projectID,
		ReceivedAt: time.Now().UTC(),
		LevelNum:   30,
		Level:      "info",
		Data:       make(map[string]any),
	}

	if msg, ok := obj["msg"].(string); ok {
		entry.Message = msg
	} else if msg, ok := obj["message"].(string); ok {
		entry.Message = msg
	}

	if lvl, ok := obj["level"]; ok {
		switch v := lvl.(type) {
		case float64:
			entry.LevelNum = int(v)
			if name, ok := levelNames[entry.LevelNum]; ok {
				entry.Level = name
			} else {
				entry.Level = strconv.Itoa(entry.LevelNum)
			}
		case string:
			entry.Level = strings.ToLower(v)
			if num, ok := levelNums[entry.Level]; ok {
				entry.LevelNum = num
			}
		}
	}

	if t, ok := obj["time"].(float64); ok {
		ms := int64(t)
		entry.ReceivedAt = time.UnixMilli(ms).UTC()
	}

	for k, v := range obj {
		if !skipFields[k] {
			entry.Data[k] = v
		}
	}
	if len(entry.Data) == 0 {
		entry.Data = nil
	}

	return entry
}

// ParseBody parses a log ingest request body into LogEntries for the given
// project. Accepts NDJSON (Content-Type application/x-ndjson) or a JSON object
// of the form {"logs": [...]}. Returns nil for an unparseable JSON body.
func ParseBody(body []byte, contentType string, projectID int64) []domain.LogEntry {
	ct := contentType
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}

	var raw []map[string]any
	switch ct {
	case "application/x-ndjson":
		scanner := bufio.NewScanner(bytes.NewReader(body))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				continue
			}
			raw = append(raw, obj)
		}
	default:
		var payload struct {
			Logs []map[string]any `json:"logs"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil
		}
		raw = payload.Logs
	}

	if len(raw) == 0 {
		return nil
	}
	entries := make([]domain.LogEntry, 0, len(raw))
	for _, obj := range raw {
		entries = append(entries, ParseObject(obj, projectID))
	}
	return entries
}

// LevelMinFromName maps a level name to its numeric pino level, 0 if unknown.
func LevelMinFromName(name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	if num, ok := levelNums[name]; ok {
		return num
	}
	return 0
}
