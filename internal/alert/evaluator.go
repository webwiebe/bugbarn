package alert

import (
	"context"
	"log"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Evaluator subscribes to the domain event bus and fires alert webhooks when
// configured conditions are met.
type Evaluator struct {
	repo      Repository
	deliverer *Deliverer
	publicURL string
}

// NewEvaluator creates an Evaluator wired to the given repository and deliverer.
func NewEvaluator(repo Repository, deliverer *Deliverer, publicURL string) *Evaluator {
	return &Evaluator{repo: repo, deliverer: deliverer, publicURL: publicURL}
}

// HandleEvent receives a domain event and dispatches to condition evaluation.
// This method is safe to pass to Bus.Subscribe directly.
func (e *Evaluator) HandleEvent(evt any) {
	ctx := context.Background()
	switch v := evt.(type) {
	case domainevents.IssueCreated:
		e.evaluate(ctx, v.ProjectID, v.Issue, "new_issue")
	case domainevents.IssueRegressed:
		e.evaluate(ctx, v.ProjectID, v.Issue, "regression")
	case domainevents.IssueEventRecorded:
		e.evaluate(ctx, v.ProjectID, v.Issue, "event_count_exceeds")
	}
}

func (e *Evaluator) evaluate(ctx context.Context, projectID int64, issue storage.Issue, conditionType string) {
	rules, err := e.repo.ListForProject(ctx, projectID)
	if err != nil {
		log.Printf("alert evaluator: list rules for project %d: %v", projectID, err)
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
		if !e.cooldownPassed(ctx, rule, issue.ID) {
			continue
		}

		// Capture loop vars for the goroutine.
		r := rule
		iss := issue
		go func() {
			fireCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := e.deliverer.Fire(fireCtx, r, iss, e.publicURL); err != nil {
				log.Printf("alert evaluator: fire alert %s for issue %s: %v", r.ID, iss.ID, err)
				return
			}

			if err := e.repo.RecordFiring(ctx, r.ID, iss.ID); err != nil {
				log.Printf("alert evaluator: record firing alert %s issue %s: %v", r.ID, iss.ID, err)
			}
			if err := e.repo.UpdateLastFired(ctx, r.ID, time.Now().UTC()); err != nil {
				log.Printf("alert evaluator: update last_fired alert %s: %v", r.ID, err)
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
		log.Printf("alert evaluator: last firing for %s/%s: %v", rule.ID, issueID, err)
		return false
	}
	if last.IsZero() {
		return true
	}
	return time.Since(last) >= time.Duration(rule.CooldownMinutes)*time.Minute
}
