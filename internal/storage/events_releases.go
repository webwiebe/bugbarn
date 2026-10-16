package storage

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// IssueReleases counts the releases among the latest sample events of an issue.
// Facets are an issue-level set and carry no counts, so the numbers come from
// the events themselves, read newest first through idx_events_issue_id. It
// returns the releases ordered by last seen and the number of events examined.
// Events without a release are examined but not listed.
func (s *EventStore) IssueReleases(ctx context.Context, issueID string, sample int) ([]domain.IssueRelease, int, error) {
	ctx, span := tracing.Tracer().Start(ctx, "storage.IssueReleases")
	defer span.End()
	span.SetAttributes(attribute.String("issue_id", issueID), attribute.Int("sample", sample))
	rowID, err := s.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return nil, 0, err
	}
	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok {
		projectID = s.defaultProjectID
	}

	rows, err := s.readDB().QueryContext(ctx, `
SELECT event_json FROM events WHERE project_id = ? AND issue_id = ? ORDER BY id DESC LIMIT ?`,
		projectID, rowID, sample)
	if err != nil {
		return nil, 0, wrapErr(err, "issue releases")
	}
	defer rows.Close()

	byRelease := map[string]*domain.IssueRelease{}
	examined := 0
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, 0, wrapErr(err, "issue releases")
		}
		examined++
		var evt event.Event
		if err := json.Unmarshal(payload, &evt); err != nil {
			continue
		}
		addRelease(byRelease, releaseOf(evt), evt.ObservedAt)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, wrapErr(err, "issue releases")
	}

	out := make([]domain.IssueRelease, 0, len(byRelease))
	for _, r := range byRelease {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].Release < out[j].Release
	})
	return out, examined, nil
}

func addRelease(byRelease map[string]*domain.IssueRelease, release string, at time.Time) {
	if release == "" {
		return
	}
	r := byRelease[release]
	if r == nil {
		r = &domain.IssueRelease{Release: release, FirstSeen: at, LastSeen: at}
		byRelease[release] = r
	}
	r.Events++
	if at.Before(r.FirstSeen) {
		r.FirstSeen = at
	}
	if at.After(r.LastSeen) {
		r.LastSeen = at
	}
}
