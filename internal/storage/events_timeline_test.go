package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

var timelineSeq int

// seedTimelineEvent inserts one event for a project with the given severity,
// receive time and sample weight, under a fresh issue.
func seedTimelineEvent(t *testing.T, s *Store, projectID int64, severity string, receivedAt time.Time, weight int) {
	t.Helper()
	ctx := context.Background()
	db := s.DB()
	timelineSeq++
	fp := fmt.Sprintf("tl-%d-%d", projectID, timelineSeq)
	res, err := db.ExecContext(ctx, `
		INSERT INTO issues (project_id, fingerprint, fingerprint_material, title, normalized_title,
			exception_type, first_seen, last_seen, event_count, representative_event_json, issue_number)
		VALUES (?, ?, '', 'issue', 'issue', 'Error', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1, '{}', 1)`,
		projectID, fp)
	if err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	issueID, _ := res.LastInsertId()
	ts := receivedAt.UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO events (project_id, issue_id, fingerprint, received_at, observed_at, severity, message, event_json, sample_weight)
		VALUES (?, ?, 'fp', ?, ?, ?, 'test', '{}', ?)`,
		projectID, issueID, ts, ts, severity, weight); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

type hourKey struct {
	slug string
	hour string
}

func timelineCounts(rows []TimelineRow) map[hourKey]int64 {
	out := map[hourKey]int64{}
	for _, r := range rows {
		out[hourKey{r.ProjectSlug, r.Hour.Format("2006-01-02T15")}] += r.Count
	}
	return out
}

func TestErrorTimelineRowsCountsPerProjectAndHour(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	alpha, err := s.EnsureProject(ctx, "alpha")
	if err != nil {
		t.Fatalf("ensure alpha: %v", err)
	}
	beta, err := s.EnsureProject(ctx, "beta")
	if err != nil {
		t.Fatalf("ensure beta: %v", err)
	}
	base := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

	seedTimelineEvent(t, s, alpha.ID, "ERROR", base.Add(5*time.Minute), 1)
	seedTimelineEvent(t, s, alpha.ID, "FATAL", base.Add(40*time.Minute), 1)
	seedTimelineEvent(t, s, alpha.ID, "ERROR", base.Add(2*time.Hour+time.Minute), 1)
	seedTimelineEvent(t, s, beta.ID, "CRITICAL", base.Add(10*time.Minute), 3) // sampled: weight 3
	seedTimelineEvent(t, s, beta.ID, "WARN", base.Add(10*time.Minute), 1)     // not an error
	seedTimelineEvent(t, s, beta.ID, "ERROR", base.Add(-3*time.Hour), 1)      // before the window

	rows, err := s.ErrorTimelineRows(ctx, base)
	if err != nil {
		t.Fatalf("ErrorTimelineRows: %v", err)
	}
	got := timelineCounts(rows)
	want := map[hourKey]int64{
		{"alpha", "2026-10-14T10"}: 2,
		{"alpha", "2026-10-14T12"}: 1,
		{"beta", "2026-10-14T10"}:  3,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%v = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

func TestErrorTimelineRowsHonoursProjectScope(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	alpha, _ := s.EnsureProject(ctx, "alpha")
	beta, _ := s.EnsureProject(ctx, "beta")
	gamma, _ := s.EnsureProject(ctx, "gamma")
	at := time.Date(2026, 10, 14, 10, 5, 0, 0, time.UTC)
	for _, p := range []Project{alpha, beta, gamma} {
		seedTimelineEvent(t, s, p.ID, "ERROR", at, 1)
	}
	since := at.Add(-time.Hour)

	one, err := s.ErrorTimelineRows(domain.WithProjectID(ctx, beta.ID), since)
	if err != nil || len(one) != 1 || one[0].ProjectSlug != "beta" {
		t.Fatalf("project scope = %v, %v; want only beta", one, err)
	}
	group, err := s.ErrorTimelineRows(domain.WithProjectIDs(domain.WithProjectID(ctx, 0), []int64{alpha.ID, gamma.ID}), since)
	if err != nil || len(group) != 2 {
		t.Fatalf("group scope = %v, %v; want alpha and gamma", group, err)
	}
	all, err := s.ErrorTimelineRows(domain.WithProjectID(ctx, 0), since)
	if err != nil || len(all) != 3 {
		t.Fatalf("all projects = %v, %v; want 3 rows", all, err)
	}
}

// The timeline reads every error event of the last 30 days. It has to stay an
// index-only read; a table scan would walk rows that carry event_json.
func TestErrorTimelinePlanIsCoveringIndex(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ErrorSeverities)), ",")
	args := []any{time.Now().UTC().Format(time.RFC3339Nano)}
	for _, sev := range ErrorSeverities {
		args = append(args, sev)
	}
	rows, err := s.DB().QueryContext(ctx, `
		EXPLAIN QUERY PLAN
		SELECT project_id, substr(received_at, 1, 13) AS hour, SUM(sample_weight)
		FROM events INDEXED BY idx_events_error_timeline WHERE received_at >= ? AND severity IN (`+marks+`)
		GROUP BY project_id, hour`, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, "COVERING INDEX idx_events_error_timeline") {
		t.Errorf("timeline query is not answered from idx_events_error_timeline; plan: %s", joined)
	}
}
