package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// spooledRequestAt pairs a decoded spooledRequest with the byte offset of the
// end of its line in the spool file, so the caller can advance the cursor
// past exactly the records it successfully forwarded.
type spooledRequestAt struct {
	req       spooledRequest
	endOffset int64
}

func readRecords(path string, offset int64) ([]spooledRequestAt, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, offset, nil
		}
		return nil, offset, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, offset, err
	}
	if offset > info.Size() {
		offset = 0
	}
	if offset > 0 {
		if _, err := file.Seek(offset, 0); err != nil {
			return nil, offset, err
		}
	}
	var out []spooledRequestAt
	pos := offset
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		pos += int64(len(line)) + 1
		if len(line) == 0 {
			continue
		}
		var rec spooledRequest
		if err := json.Unmarshal(line, &rec); err != nil {
			// corrupt line (e.g. truncated write during pod restart) — skip it
			slog.Warn("spool: skipping corrupt record", "offset", pos, "error", err)
			continue
		}
		out = append(out, spooledRequestAt{req: rec, endOffset: pos})
	}
	if err := scanner.Err(); err != nil {
		return nil, pos, err
	}
	return out, pos, nil
}

type cursorState struct {
	Offset int64 `json:"offset"`
}

func readCursor(dir string) (int64, error) {
	data, err := os.ReadFile(filepath.Join(dir, spoolCursorFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var c cursorState
	if err := json.Unmarshal(data, &c); err != nil {
		return 0, err
	}
	return c.Offset, nil
}

func writeCursor(dir string, offset int64) error {
	data, err := json.Marshal(cursorState{Offset: offset})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, spoolCursorFileName), data, 0o600)
}

func resetCursor(dir string) error {
	err := os.Remove(filepath.Join(dir, spoolCursorFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
