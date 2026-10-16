package storage

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

const (
	autoReleaseCreator = "auto"
	// seenReleasesCap bounds the in-memory (project, release, environment) set.
	// When it fills up it is cleared; the lookup in ensureRelease keeps the
	// result correct and the next event per release pays one primary index read.
	seenReleasesCap = 10000
)

type seenReleases struct {
	mu sync.Mutex
	m  map[string]struct{}
}

func (s *seenReleases) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[key]
	return ok
}

func (s *seenReleases) add(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil || len(s.m) >= seenReleasesCap {
		s.m = make(map[string]struct{})
	}
	s.m[key] = struct{}{}
}

// ensureRelease records a release row the first time a project's events carry a
// release value. A release that was posted by hand for the same version (and
// environment, when the posted one names none or the same one) is reused. Events
// without a release value create nothing. Failures are logged and never fail the
// ingest. It runs only on the writer persistence path, so readers never write.
func (s *core) ensureRelease(ctx context.Context, projectID int64, evt event.Event) {
	version := strings.TrimSpace(releaseOf(evt))
	if version == "" {
		return
	}
	env := releaseEnvironmentOf(evt)
	key := releaseSeenKey(projectID, version, env)
	if s.seenReleases.has(key) {
		return
	}

	var exists int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1) FROM releases
WHERE project_id = ? AND (version = ? OR name = ?) AND (? = '' OR environment = '' OR environment = ?)`,
		projectID, version, version, env, env).Scan(&exists)
	if err != nil {
		slog.WarnContext(ctx, "storage: release lookup failed", "project_id", projectID, "error", err)
		return
	}
	if exists == 0 {
		observed := evt.ObservedAt
		if observed.IsZero() {
			observed = evt.ReceivedAt
		}
		if observed.IsZero() {
			observed = time.Now().UTC()
		}
		_, err = s.db.ExecContext(ctx, `
INSERT INTO releases (project_id, name, environment, observed_at, version, commit_sha, url, notes, created_by)
VALUES (?, ?, ?, ?, ?, '', '', '', ?)`,
			projectID, version, env, formatTime(observed), version, autoReleaseCreator)
		if err != nil {
			slog.WarnContext(ctx, "storage: automatic release insert failed", "project_id", projectID, "error", err)
			return
		}
	}
	s.seenReleases.add(key)
}

func releaseEnvironmentOf(evt event.Event) string {
	if env := stringFromMap(evt.Attributes, "deployment.environment"); env != "" {
		return env
	}
	return stringFromMap(evt.Resource, "deployment.environment")
}

func releaseSeenKey(projectID int64, version, env string) string {
	return strings.Join([]string{strconv.FormatInt(projectID, 10), version, env}, "\x00")
}
