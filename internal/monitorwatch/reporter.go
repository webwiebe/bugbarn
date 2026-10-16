// Package monitorwatch turns monitor incidents into BugBarn issues. A missed or
// failed check-in becomes an event in the monitor's own project, so it reaches
// the same alert path as an application error. A recovery records one more
// event on the open issue and resolves it.
package monitorwatch

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	ProjectByID(ctx context.Context, id int64) (storage.Project, error)
	IssueByFingerprint(ctx context.Context, projectID int64, fingerprint string) (domain.Issue, error)
	ResolveIssue(ctx context.Context, issueID string) (domain.Issue, error)
}

// Statuses of an open issue; a resolved or muted one is left alone.
const (
	issueUnresolved = "unresolved"
	issueRegressed  = "regressed"
)

// Reporter writes monitor incidents as events and resolves them on recovery.
type Reporter struct {
	store   IssueStore
	persist Persist
	log     *slog.Logger
	now     func() time.Time
}

// NewReporter builds a reporter that persists through persist.
func NewReporter(store IssueStore, persist Persist, log *slog.Logger) *Reporter {
	return &Reporter{store: store, persist: persist, log: log.With("component", "monitors"), now: time.Now}
}

// Missed reports a monitor that did not check in on time.
func (r *Reporter) Missed(ctx context.Context, m domain.Monitor) error {
	overdue := r.now().Sub(m.NextDue()).Round(time.Second)
	attrs := monitorAttrs(m)
	attrs["overdue_seconds"] = int64(overdue / time.Second)
	return r.emit(ctx, m, domain.MonitorStateMissed, "Monitor missed: "+m.Slug, "error", attrs)
}

// Errored reports a check-in with status "error". The message is the cleaned
// free text of that check-in and lives only in the event.
func (r *Reporter) Errored(ctx context.Context, m domain.Monitor, message string) error {
	attrs := monitorAttrs(m)
	if message != "" {
		attrs["message"] = message
	}
	return r.emit(ctx, m, domain.MonitorStateError, "Monitor failed: "+m.Slug, "error", attrs)
}

// Recovered records the recovery on each open issue of the monitor and
// resolves it. A resolved or muted issue is left alone, so a recovery never
// reopens what someone already handled. The event goes in before the resolve,
// otherwise it would regress the issue it was meant to close.
func (r *Reporter) Recovered(ctx context.Context, m domain.Monitor, at time.Time) error {
	var errs []error
	for _, kind := range []string{domain.MonitorStateMissed, domain.MonitorStateError} {
		issue, err := r.store.IssueByFingerprint(ctx, m.ProjectID, Fingerprint(m.Slug, kind))
		if errors.Is(err, apperr.ErrNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if issue.Status != issueUnresolved && issue.Status != issueRegressed {
			continue
		}
		attrs := monitorAttrs(m)
		attrs["recovered_at"] = at.UTC().Format(time.RFC3339)
		if err := r.emit(ctx, m, kind, "Monitor recovered: "+m.Slug, "info", attrs); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := r.store.ResolveIssue(ctx, issue.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Fingerprint is the issue identity of a monitor incident: one issue per
// monitor and kind, so repeats land on it and a resolved one regresses.
func Fingerprint(slug, kind string) string {
	return fmt.Sprintf("monitor:%s:%s", slug, kind)
}

func monitorAttrs(m domain.Monitor) map[string]any {
	attrs := map[string]any{
		"slug":             m.Slug,
		"interval_seconds": int64(m.Interval / time.Second),
		"grace_seconds":    int64(m.Grace / time.Second),
	}
	if m.Schedule != "" {
		attrs["schedule"] = m.Schedule
		attrs["timezone"] = m.Timezone
	}
	if !m.LastCheckInAt.IsZero() {
		attrs["last_checkin_at"] = m.LastCheckInAt.UTC().Format(time.RFC3339)
	}
	return attrs
}

func (r *Reporter) emit(ctx context.Context, m domain.Monitor, kind, title, severity string, attrs map[string]any) error {
	slug := m.ProjectSlug
	if slug == "" {
		proj, err := r.store.ProjectByID(ctx, m.ProjectID)
		if err != nil {
			return err
		}
		slug = proj.Slug
	}
	body, err := json.Marshal(map[string]any{
		"body":              title,
		"severityText":      severity,
		"observedTimestamp": r.now().UTC().Format(time.RFC3339Nano),
		"fingerprint":       Fingerprint(m.Slug, kind),
		"attributes":        map[string]any{"monitor": attrs},
		"resource":          resourceFor(m.Slug),
	})
	if err != nil {
		return err
	}
	return r.persist(ctx, spool.Record{
		IngestID:    "monitor-" + randomID(),
		ReceivedAt:  r.now().UTC(),
		ContentType: "application/json",
		BodyBase64:  base64.StdEncoding.EncodeToString(body),
		ProjectSlug: slug,
		Internal:    true,
	})
}

// environments a monitor slug can end in, as in "appointment-reminders-testing".
var slugEnvironments = []string{"production", "staging", "testing", "e2e", "development"}

// resourceFor is the event resource of a monitor incident. A monitor has no
// environment field, so it is read from the slug suffix; a slug without one
// leaves the event without an environment.
func resourceFor(slug string) map[string]any {
	res := map[string]any{"service.name": "bugbarn-monitors"}
	for _, env := range slugEnvironments {
		if strings.HasSuffix(slug, "-"+env) {
			res["deployment.environment"] = env
			break
		}
	}
	return res
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
