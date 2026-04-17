package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func formatTime(value time.Time) string {
	return value.UTC().Format(timeLayout)
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeLayout, value)
}

func formatID(prefix string, value int64) string {
	return fmt.Sprintf("%s%06d", prefix, value)
}

func parseID(prefix, value string) (int64, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, prefix) {
		return 0, fmt.Errorf("invalid id %q", value)
	}
	return strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
}

func sqliteDSN(path string) string {
	u := url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}
	return u.String() + "?cache=shared&mode=rwc&_busy_timeout=5000"
}

func marshalEvent(evt event.Event) ([]byte, error) {
	return json.Marshal(evt)
}

func fingerprintFromEvent(evt event.Event) string {
	exceptionType := strings.TrimSpace(evt.Exception.Type)
	message := strings.TrimSpace(evt.Exception.Message)
	if message == "" {
		message = strings.TrimSpace(evt.Message)
	}

	var title string
	switch {
	case exceptionType != "" && message != "":
		title = exceptionType + ": " + message
	case exceptionType != "":
		title = exceptionType
	default:
		title = message
	}

	return normalizeTitle(title)
}

func mustMarshalStrings(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func unmarshalStringSlice(raw []byte, dest *[]string) error {
	if len(raw) == 0 {
		*dest = nil
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		*dest = nil
		return err
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
