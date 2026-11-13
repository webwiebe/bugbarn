package silence

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Persist stores one event record; a nil error means the event is in the
// project (or held for an unapproved project).
type Persist func(ctx context.Context, rec spool.Record) error

// IssueStore is the part of the store the reporter needs.
type IssueStore interface {
	ProjectBySlug(ctx context.Context, slug string) (storage.Project, error)
	IssueByFingerprint(ctx context.Context, projectID int64, fingerprint string) (domain.Issue, error)
	ResolveIssue(ctx context.Context, issueID string) (domain.Issue, error)
}

// Issue statuses the reporter distinguishes. A resolved issue regresses when a
// new event arrives; an unresolved, regressed or muted one is left alone.
const issueResolved = "resolved"

// IssueFingerprint is the issue identity of one silent project and rule: one
// issue per project and rule, so a repeat lands on it and a resolved one
// regresses. The "silence:" prefix keeps these issues out of the activity the
// rules count (see storage.selfFiledIssuesSQL).
func IssueFingerprint(slug, rule string) string {
	return fmt.Sprintf("silence:%s:%s", slug, rule)
}

// Reporter files one issue per silent project and rule into a target project
// (infra) and resolves it when the stream reports again. It is the Filer of
// file mode.
type Reporter struct {
	store   IssueStore
	persist Persist
	project string
	log     *slog.Logger
	now     func() time.Time
}

// NewReporter builds a reporter that files into the project with the slug
// project, persisting through persist.
func NewReporter(store IssueStore, persist Persist, project string, log *slog.Logger) *Reporter {
	return &Reporter{store: store, persist: persist, project: project, log: log, now: time.Now}
}

// Fire files the issue unless one is already open or muted.
func (r *Reporter) Fire(ctx context.Context, f Finding) error {
	target, err := r.store.ProjectBySlug(ctx, r.project)
	if err != nil {
		return fmt.Errorf("silence: target project %q: %w", r.project, err)
	}
	issue, err := r.store.IssueByFingerprint(ctx, target.ID, IssueFingerprint(f.Slug, f.Rule))
	switch {
	case err == nil && issue.Status != issueResolved:
		return nil
	case err != nil && !errors.Is(err, apperr.ErrNotFound):
		return err
	}
	title := fmt.Sprintf("Project silent: %s (%s)", f.Slug, f.Rule)
	return r.emit(ctx, f.Slug, f.Rule, title, "error", findingAttrs(f))
}

// Resume records one more event on the open issue and resolves it. The event
// goes in before the resolve, otherwise it would regress the issue it closes.
func (r *Reporter) Resume(ctx context.Context, slug, rule string) error {
	target, err := r.store.ProjectBySlug(ctx, r.project)
	if err != nil {
		return fmt.Errorf("silence: target project %q: %w", r.project, err)
	}
	issue, err := r.store.IssueByFingerprint(ctx, target.ID, IssueFingerprint(slug, rule))
	if errors.Is(err, apperr.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if issue.Status == issueResolved || issue.Status == "muted" {
		return nil
	}
	attrs := map[string]any{"project": slug, "rule": rule, "resumed_at": r.now().UTC().Format(time.RFC3339)}
	if err := r.emit(ctx, slug, rule, fmt.Sprintf("Project reporting again: %s (%s)", slug, rule), "info", attrs); err != nil {
		return err
	}
	_, err = r.store.ResolveIssue(ctx, issue.ID)
	return err
}

func findingAttrs(f Finding) map[string]any {
	attrs := map[string]any{
		"project":           f.Slug,
		"rule":              f.Rule,
		"gap_hours":         f.Gap.Hours(),
		"longest_gap_hours": f.LongestGap.Hours(),
		"volume":            f.Volume,
		"key_last_used":     formatKeyUse(f.KeyLastUsed),
	}
	return attrs
}

func (r *Reporter) emit(ctx context.Context, slug, rule, title, severity string, attrs map[string]any) error {
	body, err := json.Marshal(map[string]any{
		"body":              title,
		"severityText":      severity,
		"observedTimestamp": r.now().UTC().Format(time.RFC3339Nano),
		"fingerprint":       IssueFingerprint(slug, rule),
		"attributes":        map[string]any{"silence": attrs},
		"resource":          map[string]any{"service.name": "bugbarn-silence"},
	})
	if err != nil {
		return err
	}
	return r.persist(ctx, spool.Record{
		IngestID:    "silence-" + randomID(),
		ReceivedAt:  r.now().UTC(),
		ContentType: "application/json",
		BodyBase64:  base64.StdEncoding.EncodeToString(body),
		ProjectSlug: r.project,
		Internal:    true,
	})
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// LogInserter stores log entries.
type LogInserter interface {
	InsertLogEntries(ctx context.Context, entries []storage.LogEntry) error
}

// ProjectLookup resolves a project slug.
type ProjectLookup interface {
	ProjectBySlug(ctx context.Context, slug string) (storage.Project, error)
}

// LogShipper writes the "silence rule would fire" line as a log entry of
// BugBarn's own project, the one selflog reports to, so it shows in `bb logs`
// when nobody can read the writer's stdout.
type LogShipper struct {
	logs     LogInserter
	projects ProjectLookup
	project  string
	log      *slog.Logger
	now      func() time.Time
}

// NewLogShipper builds a shipper that writes into the project with the slug
// project. An empty slug disables it.
func NewLogShipper(logs LogInserter, projects ProjectLookup, project string, log *slog.Logger) *LogShipper {
	return &LogShipper{logs: logs, projects: projects, project: project, log: log, now: time.Now}
}

// Ship stores one info-level log entry for the finding. A failure is logged at
// warn level: the line is a diagnostic, nothing is lost when it is missing.
func (s *LogShipper) Ship(ctx context.Context, f Finding, mode Mode) {
	if s.project == "" {
		return
	}
	p, err := s.projects.ProjectBySlug(ctx, s.project)
	if err != nil {
		s.log.Warn("silence rules: ship line: project", "project", s.project, "error", err)
		return
	}
	attrs := findingAttrs(f)
	attrs["mode"] = string(mode)
	attrs["component"] = "silence-rules"
	entry := storage.LogEntry{
		ProjectID:  p.ID,
		ReceivedAt: s.now().UTC(),
		LevelNum:   30,
		Level:      "info",
		Message:    "silence rule would fire",
		Data:       attrs,
	}
	if err := s.logs.InsertLogEntries(ctx, []storage.LogEntry{entry}); err != nil {
		s.log.Warn("silence rules: ship line: insert", "project", s.project, "error", err)
	}
}

// ShipSweep stores one info-level entry with the sweep's counts: how many
// projects were checked and how many rules would fire. It carries no project
// name. Without it, an hour in which nothing fires leaves no trace in `bb logs`
// and cannot be told from a monitor that does not run.
func (s *LogShipper) ShipSweep(ctx context.Context, mode Mode, projects, wouldFire int) {
	if s.project == "" {
		return
	}
	p, err := s.projects.ProjectBySlug(ctx, s.project)
	if err != nil {
		s.log.Warn("silence rules: ship sweep: project", "project", s.project, "error", err)
		return
	}
	entry := storage.LogEntry{
		ProjectID:  p.ID,
		ReceivedAt: s.now().UTC(),
		LevelNum:   30,
		Level:      "info",
		Message:    "silence rules swept",
		Data: map[string]any{
			"mode": string(mode), "projects": projects, "would_fire": wouldFire, "component": "silence-rules",
		},
	}
	if err := s.logs.InsertLogEntries(ctx, []storage.LogEntry{entry}); err != nil {
		s.log.Warn("silence rules: ship sweep: insert", "project", s.project, "error", err)
	}
}
