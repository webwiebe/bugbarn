package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// A lookup a reader could not serve (SQLITE_READONLY_RECOVERY after its
// retries, #261) failed a request with a 503. It logs one ERROR, which selflog
// files, and the line names the caller.
func TestBySlugUnavailableLogsErrorWithCaller(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := New(&fakeRepo{err: apperr.Unavailable("project not found", errors.New("(264)"))}, logger)

	ctx := domain.WithRequestInfo(context.Background(), domain.RequestInfo{
		Route: "POST /api/v1/monitors/nightly/check-in", UserAgent: "bugbarn-go/1.0",
	})
	_, err := svc.BySlug(ctx, "profotograaf")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("want one JSON log line, got %q: %v", buf.String(), err)
	}
	for key, want := range map[string]string{
		"level":      "ERROR",
		"msg":        "project by slug: database unavailable on this reader",
		"slug":       "profotograaf",
		"route":      "POST /api/v1/monitors/nightly/check-in",
		"user_agent": "bugbarn-go/1.0",
	} {
		if got, _ := line[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestBySlugInternalErrorStillLogsError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	svc := New(&fakeRepo{err: apperr.Internal("project not found", errors.New("disk I/O error"))}, logger)

	if _, err := svc.BySlug(context.Background(), "profotograaf"); err == nil {
		t.Fatal("want an error")
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("want one JSON log line, got %q: %v", buf.String(), err)
	}
	if line["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", line["level"])
	}
}
