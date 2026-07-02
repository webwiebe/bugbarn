package alert

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
)

// notifier delivers alert notifications. *Deliverer is the production
// implementation; tests substitute a fake.
type notifier interface {
	Fire(ctx context.Context, rule Rule, issue domain.Issue, publicURL string) error
	EmailConfigured() bool
}

// Evaluator subscribes to the domain event bus and fires alert webhooks when
// configured conditions are met.
type Evaluator struct {
	repo       Repository
	deliverer  notifier
	publicURL  string
	adminEmail string
	log        *slog.Logger
}

// NewEvaluator creates an Evaluator wired to the given repository and deliverer.
// When adminEmail is set and SMTP is configured, every new issue and regression
// is emailed to that address regardless of per-project alert rules.
func NewEvaluator(repo Repository, deliverer notifier, publicURL, adminEmail string, log *slog.Logger) *Evaluator {
	return &Evaluator{repo: repo, deliverer: deliverer, publicURL: publicURL, adminEmail: adminEmail, log: log}
}

// HandleEvent receives a domain event and dispatches to condition evaluation.
// This method is safe to pass to Bus.Subscribe directly.
func (e *Evaluator) HandleEvent(evt any) {
	ctx := context.Background()
	switch v := evt.(type) {
	case domainevents.IssueCreated:
		e.evaluate(ctx, v.ProjectID, v.Issue, "new_issue")
		e.evaluate(ctx, v.ProjectID, v.Issue, "message_contains")
		e.notifyAdmin(v.Issue, "new_issue")
	case domainevents.IssueRegressed:
		e.evaluate(ctx, v.ProjectID, v.Issue, "regression")
		e.evaluate(ctx, v.ProjectID, v.Issue, "message_contains")
		e.notifyAdmin(v.Issue, "regression")
	case domainevents.IssueEventRecorded:
		e.evaluate(ctx, v.ProjectID, v.Issue, "event_count_exceeds")
	}
}

// notifyAdmin emails the configured admin address for every new issue and
// regression, across all projects. It is independent of per-project alert rules
// so newly created projects are covered automatically. No-op when no admin
// address is configured or SMTP is not set up.
func (e *Evaluator) notifyAdmin(issue domain.Issue, conditionType string) {
	if e.adminEmail == "" || !e.deliverer.EmailConfigured() {
		return
	}

	rule := Rule{
		ID:        "admin-" + conditionType,
		Name:      "Admin notifications",
		Enabled:   true,
		EmailTo:   e.adminEmail,
		Condition: conditionType,
	}
	iss := issue

	go func() {
		defer func() {
			if p := recover(); p != nil {
				e.log.Error("admin alert delivery panic", "issue_id", iss.ID, "condition", conditionType, "panic", p)
			}
		}()

		fireCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := e.deliverer.Fire(fireCtx, rule, iss, e.publicURL); err != nil {
			e.log.Error("failed to fire admin alert", "issue_id", iss.ID, "condition", conditionType, "error", err)
		}
	}()
}

func (e *Evaluator) evaluate(ctx context.Context, projectID int64, issue domain.Issue, conditionType string) {
	rules, err := e.repo.ListForProject(ctx, projectID)
	if err != nil {
		e.log.Error("failed to list rules for project", "project_id", projectID, "error", err)
		return
	}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if rule.Condition != conditionType {
			continue
		}
		if conditionType == "event_count_exceeds" && (rule.Threshold <= 0 || issue.EventCount <= rule.Threshold) {
			continue
		}
		if conditionType == "message_contains" {
			if rule.Param == "" {
				continue
			}
			if !strings.Contains(strings.ToLower(issue.Title), strings.ToLower(rule.Param)) {
				continue
			}
		}
		if !e.cooldownPassed(ctx, rule, issue.ID) {
			continue
		}

		// Capture loop vars for the goroutine.
		r := rule
		iss := issue
		go func() {
			defer func() {
				if p := recover(); p != nil {
					e.log.Error("alert delivery panic", "alert_id", r.ID, "issue_id", iss.ID, "panic", p)
				}
			}()

			fireCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := e.deliverer.Fire(fireCtx, r, iss, e.publicURL); err != nil {
				e.log.Error("failed to fire alert", "alert_id", r.ID, "issue_id", iss.ID, "error", err)
				return
			}

			if err := e.repo.RecordFiring(ctx, r.ID, iss.ID); err != nil {
				e.log.Error("failed to record firing", "alert_id", r.ID, "issue_id", iss.ID, "error", err)
			}
			if err := e.repo.UpdateLastFired(ctx, r.ID, time.Now().UTC()); err != nil {
				e.log.Error("failed to update last_fired", "alert_id", r.ID, "error", err)
			}
		}()
	}
}

// cooldownPassed returns true when enough time has elapsed since the last firing
// for this alert/issue pair to fire again.
func (e *Evaluator) cooldownPassed(ctx context.Context, rule Rule, issueID string) bool {
	if rule.CooldownMinutes <= 0 {
		return true
	}
	last, err := e.repo.LastFiring(ctx, rule.ID, issueID)
	if err != nil {
		e.log.Error("failed to check last firing", "alert_id", rule.ID, "issue_id", issueID, "error", err)
		return false
	}
	if last.IsZero() {
		return true
	}
	return time.Since(last) >= time.Duration(rule.CooldownMinutes)*time.Minute
}
