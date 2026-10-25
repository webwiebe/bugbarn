package alert

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// newIssueSuppressedCounter counts new issues whose mail was held back by the
// per-project cap, by project.
var newIssueSuppressedCounter metric.Int64Counter

func init() {
	newIssueSuppressedCounter, _ = tracing.Meter().Int64Counter(
		"bugbarn.alert.new_issue_suppressed",
		metric.WithDescription("New-issue mails held back by the per-project cap and reported in a summary."),
		metric.WithUnit("{issue}"),
	)
}

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
	newIssues  *newIssueLimiter
}

// NewEvaluator creates an Evaluator wired to the given repository and deliverer.
// When adminEmail is set and SMTP is configured, every new issue and regression
// is emailed to that address regardless of per-project alert rules.
func NewEvaluator(repo Repository, deliverer notifier, publicURL, adminEmail string, log *slog.Logger) *Evaluator {
	return &Evaluator{
		repo: repo, deliverer: deliverer, publicURL: publicURL, adminEmail: adminEmail, log: log,
		newIssues: newNewIssueLimiter(),
	}
}

// HandleEvent receives a domain event and dispatches to condition evaluation.
// This method is safe to pass to Bus.Subscribe directly.
func (e *Evaluator) HandleEvent(evt any) {
	ctx := context.Background()
	switch v := evt.(type) {
	case domainevents.IssueCreated:
		capped := !e.admitNewIssue(v.Issue)
		e.evaluateRules(ctx, v.ProjectID, v.Issue, "new_issue", capped)
		e.evaluate(ctx, v.ProjectID, v.Issue, "message_contains")
		if !capped {
			e.notifyAdmin(v.Issue, "new_issue")
		}
	case domainevents.IssueRegressed:
		e.evaluate(ctx, v.ProjectID, v.Issue, "regression")
		e.evaluate(ctx, v.ProjectID, v.Issue, "message_contains")
		e.notifyAdmin(v.Issue, "regression")
	case domainevents.IssueEventRecorded:
		e.evaluate(ctx, v.ProjectID, v.Issue, "event_count_exceeds")
		if isSpikeMilestone(v.Issue.EventCount) {
			e.notifyAdmin(v.Issue, "event_spike")
		}
	}
}

// isSpikeMilestone reports whether an issue's lifetime event count has just
// reached a power of ten from 100 up. A loop on an existing issue never trips the
// new-issue or regression mails; these milestones (100, 1000, 10000, ...) mail the
// admin once per order of magnitude. Counts that skip past a milestone, as sampled
// issues can, do not mail.
func isSpikeMilestone(count int) bool {
	if count < 100 {
		return false
	}
	for count%10 == 0 {
		count /= 10
	}
	return count == 1
}

// admitNewIssue counts a new issue against its project's mail cap and reports
// whether its own mail may go out. Past newIssueMailMax in the current window the
// issue is counted and reported in one summary mail when the window ends, so a
// grouping gap that opens an issue per event costs a handful of mails, not one
// per event. The cap covers the admin mail and every project rule that sends
// email; webhook-only rules are not mail and are not capped.
func (e *Evaluator) admitNewIssue(issue domain.Issue) bool {
	slug := issue.ProjectSlug
	a := e.newIssues.admit(slug)
	if a.Summarize > 0 {
		e.notifySuppressed(slug, a.Summarize)
	}
	if !a.Send {
		newIssueSuppressedCounter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("project", slug)))
	}
	if a.ScheduleSummary {
		start := a.WindowStart
		time.AfterFunc(a.Remaining, func() {
			if n := e.newIssues.expire(slug, start); n > 0 {
				e.notifySuppressed(slug, n)
			}
		})
	}
	return a.Send
}

// notifySuppressed mails one summary for new issues that were not mailed
// individually.
func (e *Evaluator) notifySuppressed(slug string, count int) {
	e.log.Info("new issue mails capped", "project", slug, "suppressed", count)
	minutes := int(newIssueMailWindow.Minutes())
	title := fmt.Sprintf("%d more new issues in the last %d minutes were not mailed one by one", count, minutes)
	e.notifyAdmin(domain.Issue{Title: title, ProjectSlug: slug}, "new_issue_flood")
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
			return
		}
		e.log.Info("admin alert sent", "issue_id", iss.ID, "project", iss.ProjectSlug, "condition", conditionType)
	}()
}

func (e *Evaluator) evaluate(ctx context.Context, projectID int64, issue domain.Issue, conditionType string) {
	e.evaluateRules(ctx, projectID, issue, conditionType, false)
}

// ruleMatches reports whether rule applies to issue for conditionType, before
// the cooldown check. With mailCapped set, rules that send email never match.
func ruleMatches(rule Rule, issue domain.Issue, conditionType string, mailCapped bool) bool {
	if !rule.Enabled || rule.Condition != conditionType {
		return false
	}
	if mailCapped && rule.EmailTo != "" {
		return false
	}
	switch conditionType {
	case "event_count_exceeds":
		return rule.Threshold > 0 && issue.EventCount > rule.Threshold
	case "message_contains":
		return rule.Param != "" && strings.Contains(strings.ToLower(issue.Title), strings.ToLower(rule.Param))
	}
	return true
}

// evaluateRules fires the matching project rules. With mailCapped set, rules
// that send email are skipped (the project is over its new-issue mail cap).
func (e *Evaluator) evaluateRules(ctx context.Context, projectID int64, issue domain.Issue, conditionType string, mailCapped bool) {
	rules, err := e.repo.ListForProject(ctx, projectID)
	if err != nil {
		e.log.Error("failed to list rules for project", "project_id", projectID, "error", err)
		return
	}

	for _, rule := range rules {
		if !ruleMatches(rule, issue, conditionType, mailCapped) {
			continue
		}
		if !e.cooldownPassed(ctx, rule, issue.ID) {
			continue
		}

		go e.fireRule(ctx, rule, issue)
	}
}

// fireRule delivers one rule's alert and records the firing. It runs in its own
// goroutine, so a panic is recovered and logged here.
func (e *Evaluator) fireRule(ctx context.Context, r Rule, iss domain.Issue) {
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
	e.log.Info("alert sent", "alert_id", r.ID, "issue_id", iss.ID, "project", iss.ProjectSlug, "condition", r.Condition)

	if err := e.repo.RecordFiring(ctx, r.ID, iss.ID); err != nil {
		e.log.Error("failed to record firing", "alert_id", r.ID, "issue_id", iss.ID, "error", err)
	}
	if err := e.repo.UpdateLastFired(ctx, r.ID, time.Now().UTC()); err != nil {
		e.log.Error("failed to update last_fired", "alert_id", r.ID, "error", err)
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
