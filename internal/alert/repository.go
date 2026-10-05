package alert

import (
	"context"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Repository defines the data access contract for alert rules and firings.
type Repository interface {
	ListForProject(ctx context.Context, projectID int64) ([]Rule, error)
	RecordFiring(ctx context.Context, alertID, issueID string) error
	LastFiring(ctx context.Context, alertID, issueID string) (time.Time, error)
	UpdateLastFired(ctx context.Context, alertID string, firedAt time.Time) error
}

// AlertSource is the slice of the storage layer the evaluator reads and writes.
// *storage.AlertStore satisfies it.
type AlertSource interface {
	ListAlertsForProject(ctx context.Context, projectID int64) ([]domain.Alert, error)
	RecordAlertFiring(ctx context.Context, alertID, issueID string) error
	LastAlertFiring(ctx context.Context, alertID, issueID string) (time.Time, error)
	UpdateAlertLastFired(ctx context.Context, alertID string, firedAt time.Time) error
}

// StoreRepository adapts an AlertSource to Repository, converting stored alerts
// into evaluator rules.
type StoreRepository struct {
	src AlertSource
}

// NewStoreRepository wraps the storage layer's alert store.
func NewStoreRepository(src AlertSource) *StoreRepository {
	return &StoreRepository{src: src}
}

// ListForProject returns all alert rules for a project, newest first.
func (r *StoreRepository) ListForProject(ctx context.Context, projectID int64) ([]Rule, error) {
	alerts, err := r.src.ListAlertsForProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	rules := make([]Rule, 0, len(alerts))
	for _, a := range alerts {
		rules = append(rules, ruleFromAlert(a, projectID))
	}
	return rules, nil
}

// RecordFiring stores a firing for the alert/issue pair.
func (r *StoreRepository) RecordFiring(ctx context.Context, alertID, issueID string) error {
	return r.src.RecordAlertFiring(ctx, alertID, issueID)
}

// LastFiring returns the most recent firing time for the alert/issue pair, or
// the zero time when it never fired.
func (r *StoreRepository) LastFiring(ctx context.Context, alertID, issueID string) (time.Time, error) {
	return r.src.LastAlertFiring(ctx, alertID, issueID)
}

// UpdateLastFired sets last_fired_at on the alert.
func (r *StoreRepository) UpdateLastFired(ctx context.Context, alertID string, firedAt time.Time) error {
	return r.src.UpdateAlertLastFired(ctx, alertID, firedAt)
}

func ruleFromAlert(a domain.Alert, projectID int64) Rule {
	return Rule{
		ID:              a.ID,
		Name:            a.Name,
		Enabled:         a.Enabled,
		ProjectID:       projectID,
		WebhookURL:      a.WebhookURL,
		EmailTo:         a.EmailTo,
		Condition:       a.Condition,
		Param:           a.Param,
		Threshold:       a.Threshold,
		CooldownMinutes: a.CooldownMinutes,
		LastFiredAt:     a.LastFiredAt,
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
	}
}
