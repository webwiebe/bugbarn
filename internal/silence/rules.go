package silence

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Rule names as they appear in the "would fire" log line.
const (
	RuleLogs   = "logs"
	RuleEvents = "events"
)

// Mode says what the rules do with a finding. Dry run logs one info line per
// finding and files and mails nothing. File also files one issue per silent
// project and rule, and resolves it when the stream resumes. Dry run is the
// default; the flip to file waits for about a week of dry-run lines and a
// threshold rescore from them (bugbarn#252).
type Mode string

const (
	ModeOff    Mode = "off"
	ModeDryRun Mode = "dry-run"
	ModeFile   Mode = "file"
)

// ParseMode reads BUGBARN_SILENCE_RULES. Empty means dry run; anything else
// that is not "dry-run" or "file" turns the rules off, so a typo never starts
// something.
func ParseMode(value string) Mode {
	switch value {
	case "", string(ModeDryRun):
		return ModeDryRun
	case string(ModeFile):
		return ModeFile
	default:
		return ModeOff
	}
}

// RulesConfig holds the revised thresholds from the 2026-11-01 comments on #252.
type RulesConfig struct {
	Mode     Mode
	Interval time.Duration

	// Logs: at least LogMinRows rows in the LogWindow ending at the newest row,
	// then a gap over LogMinGap and over LogGapFactor times the longest gap in
	// the retained rows, and at most MaxGap (past that the project is dead).
	LogMinRows   int
	LogWindow    time.Duration
	LogMinGap    time.Duration
	LogGapFactor float64
	// LogReadLimit caps how many log rows are read for a project past the gap.
	LogReadLimit int

	// Events: a gap of at least EventMinGap and over EventGapFactor times the
	// longest gap in EventHistory, at most MaxGap, with events on at least
	// EventMinDays distinct UTC days in the history before the gap.
	EventMinGap    time.Duration
	EventGapFactor float64
	EventHistory   time.Duration
	EventMinDays   int
	// EventReadLimit caps how many event times are read for a project past the gap.
	EventReadLimit int

	// MaxGap ends the judgement: a project silent for longer is dead, not silent.
	MaxGap time.Duration
}

// DefaultRulesConfig returns the revised thresholds.
func DefaultRulesConfig() RulesConfig {
	return RulesConfig{
		Mode:           ModeDryRun,
		Interval:       time.Hour,
		LogMinRows:     150,
		LogWindow:      72 * time.Hour,
		LogMinGap:      6 * time.Hour,
		LogGapFactor:   3,
		LogReadLimit:   10000,
		EventMinGap:    7 * 24 * time.Hour,
		EventGapFactor: 3,
		EventHistory:   30 * 24 * time.Hour,
		EventMinDays:   5,
		EventReadLimit: 20000,
		MaxGap:         30 * 24 * time.Hour,
	}
}

// SignalSource supplies the per-project signals the rules read. Every method
// is a bounded index read; the row lists are read only for a project that is
// already past the rule's minimum gap.
type SignalSource interface {
	ActiveProjects(ctx context.Context) ([]Project, error)
	NewestLogAt(ctx context.Context, projectID int64) (time.Time, bool, error)
	RecentLogTimes(ctx context.Context, projectID int64, limit int) ([]time.Time, error)
	NewestEventAt(ctx context.Context, projectID int64) (time.Time, bool, error)
	EventTimesSince(ctx context.Context, projectID int64, since time.Time, limit int) ([]time.Time, error)
	NewestKeyUse(ctx context.Context, projectID int64) (time.Time, bool, error)
}

// Project is an active project the rules judge.
type Project struct {
	ID   int64
	Slug string
}

// Finding is one rule that would fire for one project.
type Finding struct {
	Slug string
	Rule string
	// Gap is how long the stream has been silent.
	Gap time.Duration
	// LongestGap is the longest gap in the history the rule compared against.
	LongestGap time.Duration
	// Volume is the rows (logs) or distinct days (events) the history held.
	Volume int
	// KeyLastUsed is the newest ingest key use, evidence only. Zero when none.
	KeyLastUsed time.Time
}

// Filer files and resolves the issue of one silent project and rule. It is
// called only in file mode.
type Filer interface {
	// Fire records that the rule fires for the project. An issue that is
	// already open stays as it is.
	Fire(ctx context.Context, f Finding) error
	// Resume records that the project's stream is reporting again and resolves
	// the open issue, if any.
	Resume(ctx context.Context, slug, rule string) error
}

// Shipper writes the "would fire" line somewhere a person can read it, in
// every mode but off.
type Shipper interface {
	Ship(ctx context.Context, f Finding, mode Mode)
}

// Rules evaluates the revised silence rules hourly. In dry run it logs what
// would fire per project and does nothing else; in file mode it also files
// and resolves issues through its Filer.
type Rules struct {
	cfg     RulesConfig
	src     SignalSource
	log     *slog.Logger
	filer   Filer
	shipper Shipper
}

// WithFiler sets who files and resolves issues in file mode.
func (r *Rules) WithFiler(f Filer) *Rules {
	r.filer = f
	return r
}

// WithShipper sets where the "would fire" line goes besides the process log.
func (r *Rules) WithShipper(s Shipper) *Rules {
	r.shipper = s
	return r
}

// verdict is what one rule says about one project.
type verdict int

const (
	// verdictQuiet means the rule neither fires nor sees the stream resume: the
	// stream is silent for too short or too long a time to judge, or the
	// history is too thin. An open issue stays open.
	verdictQuiet verdict = iota
	verdictFires
	// verdictResumed means the stream reported within the rule's minimum gap.
	verdictResumed
)

// NewRules builds the rules.
func NewRules(cfg RulesConfig, src SignalSource, log *slog.Logger) *Rules {
	return &Rules{cfg: cfg, src: src, log: log}
}

// Check evaluates every active project once and returns the findings. A read
// error for one project is logged at warn level and skips that project; one
// project's bad row never hides the others.
func (r *Rules) Check(ctx context.Context, now time.Time) []Finding {
	if r.cfg.Mode == ModeOff {
		return nil
	}
	projects, err := r.src.ActiveProjects(ctx)
	if err != nil {
		r.log.Error("silence rules: list projects", "error", err)
		return nil
	}
	var found []Finding
	for _, p := range projects {
		if ctx.Err() != nil {
			break
		}
		found = append(found, r.checkProject(ctx, now, p)...)
	}
	r.log.Info("silence rules swept", "mode", string(r.cfg.Mode), "projects", len(projects), "would_fire", len(found))
	return found
}

func (r *Rules) checkProject(ctx context.Context, now time.Time, p Project) []Finding {
	var out []Finding
	for _, rule := range []func(context.Context, time.Time, Project) (Finding, verdict){r.logRule, r.eventRule} {
		f, v := rule(ctx, now, p)
		switch v {
		case verdictFires:
			out = append(out, r.report(ctx, p, f))
		case verdictResumed:
			r.resume(ctx, p, f.Rule)
		case verdictQuiet:
		}
	}
	return out
}

// report logs, ships and (in file mode) files one finding and returns it with
// the key evidence added.
func (r *Rules) report(ctx context.Context, p Project, f Finding) Finding {
	if used, ok, err := r.src.NewestKeyUse(ctx, p.ID); err == nil && ok {
		f.KeyLastUsed = used
	}
	r.log.Info("silence rule would fire",
		"project", f.Slug, "rule", f.Rule,
		"gap", f.Gap.Round(time.Minute).String(),
		"longest_gap", f.LongestGap.Round(time.Minute).String(),
		"volume", f.Volume,
		"key_last_used", formatKeyUse(f.KeyLastUsed),
		"mode", string(r.cfg.Mode))
	if r.shipper != nil {
		r.shipper.Ship(ctx, f, r.cfg.Mode)
	}
	if r.cfg.Mode == ModeFile && r.filer != nil {
		if err := r.filer.Fire(ctx, f); err != nil {
			r.log.Warn("silence rules: file issue", "project", f.Slug, "rule", f.Rule, "error", err)
		}
	}
	return f
}

// resume resolves the open issue of a project whose stream reports again.
func (r *Rules) resume(ctx context.Context, p Project, rule string) {
	if r.cfg.Mode != ModeFile || r.filer == nil {
		return
	}
	if err := r.filer.Resume(ctx, p.Slug, rule); err != nil {
		r.log.Warn("silence rules: resolve issue", "project", p.Slug, "rule", rule, "error", err)
	}
}

func formatKeyUse(t time.Time) string {
	if t.IsZero() {
		return "none"
	}
	return t.UTC().Format(time.RFC3339)
}

func (r *Rules) logRule(ctx context.Context, now time.Time, p Project) (Finding, verdict) {
	newest, ok, err := r.src.NewestLogAt(ctx, p.ID)
	if err != nil {
		r.log.Warn("silence rules: newest log row", "project", p.Slug, "error", err)
		return Finding{}, verdictQuiet
	}
	if !ok {
		return Finding{}, verdictQuiet
	}
	gap := now.Sub(newest)
	if gap <= r.cfg.LogMinGap {
		return Finding{Slug: p.Slug, Rule: RuleLogs}, verdictResumed
	}
	if gap > r.cfg.MaxGap {
		return Finding{}, verdictQuiet
	}
	times, err := r.src.RecentLogTimes(ctx, p.ID, r.cfg.LogReadLimit)
	if err != nil {
		r.log.Warn("silence rules: recent log times", "project", p.Slug, "error", err)
		return Finding{}, verdictQuiet
	}
	f, fires := EvaluateLogs(r.cfg, gap, times)
	f.Slug = p.Slug
	if fires {
		return f, verdictFires
	}
	return f, verdictQuiet
}

func (r *Rules) eventRule(ctx context.Context, now time.Time, p Project) (Finding, verdict) {
	newest, ok, err := r.src.NewestEventAt(ctx, p.ID)
	if err != nil {
		r.log.Warn("silence rules: newest event", "project", p.Slug, "error", err)
		return Finding{}, verdictQuiet
	}
	if !ok {
		return Finding{}, verdictQuiet
	}
	gap := now.Sub(newest)
	if gap < r.cfg.EventMinGap {
		return Finding{Slug: p.Slug, Rule: RuleEvents}, verdictResumed
	}
	if gap > r.cfg.MaxGap {
		return Finding{}, verdictQuiet
	}
	times, err := r.src.EventTimesSince(ctx, p.ID, now.Add(-r.cfg.EventHistory), r.cfg.EventReadLimit)
	if err != nil {
		r.log.Warn("silence rules: event times", "project", p.Slug, "error", err)
		return Finding{}, verdictQuiet
	}
	f, fires := EvaluateEvents(r.cfg, gap, times)
	f.Slug = p.Slug
	if fires {
		return f, verdictFires
	}
	return f, verdictQuiet
}

// EvaluateLogs applies the log rule to a project whose newest log row is gap
// old. times holds the retained log arrival times, newest first. The rule
// fires when the 72 hours ending at the newest row held at least LogMinRows
// rows, and the gap exceeds both LogMinGap and LogGapFactor times the longest
// gap between retained rows. The row-count test uses the window ending at the
// newest row because the per-project log cap keeps a busy project's history
// short; the longest-gap test keeps an office-hours app quiet overnight.
func EvaluateLogs(cfg RulesConfig, gap time.Duration, times []time.Time) (Finding, bool) {
	f := Finding{Rule: RuleLogs, Gap: gap, LongestGap: longestGap(times)}
	if len(times) == 0 || gap <= cfg.LogMinGap || gap > cfg.MaxGap {
		return f, false
	}
	windowStart := times[0].Add(-cfg.LogWindow)
	for _, t := range times {
		if t.Before(windowStart) {
			break
		}
		f.Volume++
	}
	if f.Volume < cfg.LogMinRows {
		return f, false
	}
	return f, float64(gap) > cfg.LogGapFactor*float64(f.LongestGap)
}

// EvaluateEvents applies the event rule to a project whose newest stored event
// is gap old. times holds the stored event arrival times within EventHistory,
// newest first. The rule fires when the gap is at least EventMinGap, at most
// MaxGap and over EventGapFactor times the longest gap in the history, and the
// history holds events on at least EventMinDays distinct UTC days. The day
// floor keeps one-event-a-week projects and test projects quiet.
func EvaluateEvents(cfg RulesConfig, gap time.Duration, times []time.Time) (Finding, bool) {
	f := Finding{Rule: RuleEvents, Gap: gap, LongestGap: longestGap(times)}
	if len(times) == 0 || gap < cfg.EventMinGap || gap > cfg.MaxGap {
		return f, false
	}
	days := map[string]struct{}{}
	for _, t := range times {
		days[t.UTC().Format("2006-01-02")] = struct{}{}
	}
	f.Volume = len(days)
	if f.Volume < cfg.EventMinDays {
		return f, false
	}
	return f, float64(gap) > cfg.EventGapFactor*float64(f.LongestGap)
}

// longestGap returns the longest gap between consecutive times, newest first.
func longestGap(times []time.Time) time.Duration {
	var longest time.Duration
	for i := 1; i < len(times); i++ {
		if d := times[i-1].Sub(times[i]); d > longest {
			longest = d
		}
	}
	return longest
}

// Start runs Check on the configured interval until ctx ends.
func (r *Rules) Start(ctx context.Context, wg *sync.WaitGroup) {
	if r.cfg.Mode == ModeOff {
		return
	}
	interval := r.cfg.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				r.Check(ctx, now)
			}
		}
	}()
}

// StoreSource reads the signals from the project store.
type StoreSource struct {
	*storage.ProjectStore
}

// ActiveProjects lists the active projects.
func (s StoreSource) ActiveProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.ActiveSilenceProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Project, len(rows))
	for i, p := range rows {
		out[i] = Project{ID: p.ID, Slug: p.Slug}
	}
	return out, nil
}
