// Package detectionrules manages the detection rule set: the built-in rules
// from internal/detect plus the overrides and custom rules stored in the main
// database. On the writer it reloads the running engine after each change.
package detectionrules

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/detect"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type Repository interface {
	ListDetectionRules(context.Context) ([]storage.StoredDetectionRule, error)
	UpsertDetectionRule(ctx context.Context, id, ruleJSON string) error
	DeleteDetectionRule(ctx context.Context, id string) error
}

// RuleView is a rule as the API shows it: the effective rule plus where it
// comes from. Builtin rules can be overridden but not removed; deleting an
// override restores the shipped rule.
type RuleView struct {
	detect.Rule
	Builtin    bool       `json:"builtin"`
	Overridden bool       `json:"overridden"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type Service struct {
	repo     Repository
	logger   *slog.Logger
	onChange func([]detect.Rule)
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "detectionrules")}
}

// OnChange registers the function that receives the effective rule set after
// every change. The writer passes the engine's SetRules; readers register
// nothing, since they forward writes to the writer.
func (s *Service) OnChange(fn func([]detect.Rule)) { s.onChange = fn }

// Effective returns the rule set the engine should run: built-ins with stored
// overrides applied, then custom rules.
func (s *Service) Effective(ctx context.Context) ([]detect.Rule, error) {
	stored, _, err := s.stored(ctx)
	if err != nil {
		return nil, err
	}
	return detect.Merge(detect.Defaults(), stored), nil
}

// List returns the effective rules with their origin.
func (s *Service) List(ctx context.Context) ([]RuleView, error) {
	stored, updated, err := s.stored(ctx)
	if err != nil {
		return nil, err
	}
	merged := detect.Merge(detect.Defaults(), stored)
	out := make([]RuleView, len(merged))
	for i, r := range merged {
		v := RuleView{Rule: r, Builtin: detect.IsBuiltin(r.ID)}
		if at, ok := updated[r.ID]; ok {
			v.Overridden = v.Builtin
			at := at
			v.UpdatedAt = &at
		}
		out[i] = v
	}
	return out, nil
}

// Put stores rule under id, overriding a built-in or adding a custom rule.
func (s *Service) Put(ctx context.Context, id string, rule detect.Rule) (RuleView, error) {
	id = strings.TrimSpace(id)
	if rule.ID != "" && rule.ID != id {
		return RuleView{}, apperr.InvalidInput("rule id does not match the path", nil)
	}
	rule.ID = id
	if err := rule.Validate(); err != nil {
		return RuleView{}, apperr.InvalidInput(err.Error(), err)
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		return RuleView{}, apperr.Internal("encode detection rule", err)
	}
	if err := s.repo.UpsertDetectionRule(ctx, id, string(raw)); err != nil {
		s.logger.ErrorContext(ctx, "store detection rule", "rule", id, "error", err)
		return RuleView{}, err
	}
	s.logger.InfoContext(ctx, "detection rule stored", "rule", id, "enabled", rule.Enabled)
	s.reload(ctx)
	builtin := detect.IsBuiltin(id)
	now := time.Now().UTC()
	return RuleView{Rule: rule, Builtin: builtin, Overridden: builtin, UpdatedAt: &now}, nil
}

// Delete removes the stored rule for id: a custom rule is gone, a built-in
// goes back to its shipped definition.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.repo.DeleteDetectionRule(ctx, id); err != nil {
		if !errors.Is(err, apperr.ErrNotFound) {
			s.logger.ErrorContext(ctx, "delete detection rule", "rule", id, "error", err)
		}
		return err
	}
	s.logger.InfoContext(ctx, "detection rule deleted", "rule", id)
	s.reload(ctx)
	return nil
}

// stored decodes the stored rules. A row that no longer validates (a rule
// field changed meaning between versions) is skipped and logged, so one bad
// row cannot take every detection down.
func (s *Service) stored(ctx context.Context) ([]detect.Rule, map[string]time.Time, error) {
	rows, err := s.repo.ListDetectionRules(ctx)
	if err != nil {
		if !apperr.IsContextError(err) {
			s.logger.ErrorContext(ctx, "list detection rules", "error", err)
		}
		return nil, nil, err
	}
	rules := make([]detect.Rule, 0, len(rows))
	updated := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		var r detect.Rule
		err := json.Unmarshal([]byte(row.JSON), &r)
		if err == nil {
			r.ID = row.ID
			err = r.Validate()
		}
		if err != nil {
			s.logger.WarnContext(ctx, "stored detection rule ignored", "rule", row.ID, "error", err)
			continue
		}
		rules = append(rules, r)
		updated[row.ID] = row.UpdatedAt
	}
	return rules, updated, nil
}

// reload hands the new rule set to the engine. The change is already stored,
// so a failure here leaves the engine on the old rules until the next change
// or restart; that is worth an error.
func (s *Service) reload(ctx context.Context) {
	if s.onChange == nil {
		return
	}
	rules, err := s.Effective(context.WithoutCancel(ctx))
	if err != nil {
		s.logger.ErrorContext(ctx, "reload detection rules; engine keeps the previous set", "error", err)
		return
	}
	s.onChange(rules)
}
