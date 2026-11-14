package storage

import (
	"strings"
	"time"
)

func formatTime(value time.Time) string {
	return value.UTC().Format(timeLayout)
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(parseLayout, value)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
