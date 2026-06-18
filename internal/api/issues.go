package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const mutateTimeout = 5 * time.Second

// tryMutate races fn against a 5-second deadline.
// Returns (issue, queued=false, err) on completion, or (zero, queued=true, nil) when
// the deadline fires and the mutation was successfully queued for async application.
func tryMutate(ctx context.Context, q *mutqueue.Queue, rec mutqueue.Record, fn func(context.Context) (domain.Issue, error)) (domain.Issue, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, mutateTimeout)
	defer cancel()

	type outcome struct {
		issue domain.Issue
		err   error
	}
	ch := make(chan outcome, 1)
	go func() {
		issue, err := fn(ctx)
		ch <- outcome{issue, err}
	}()

	select {
	case o := <-ch:
		return o.issue, false, o.err
	case <-ctx.Done():
		if q != nil {
			if err := q.Append(rec); err != nil {
				return domain.Issue{}, false, fmt.Errorf("mutqueue: %w", err)
			}
		}
		return domain.Issue{}, true, nil
	}
}

// serveIssueRoute handles /api/v1/issues/{id} and sub-paths.
func (s *Server) serveIssueRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/resolve") && r.Method == http.MethodPost:
		s.resolveIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/reopen") && r.Method == http.MethodPost:
		s.reopenIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/mute") && r.Method == http.MethodPatch:
		s.muteIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/unmute") && r.Method == http.MethodPatch:
		s.unmuteIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/events"):
		s.listIssueEvents(w, r)
	default:
		s.getIssue(w, r)
	}
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Accept project_slug query param as a fallback for the X-BugBarn-Project header.
	// This lets the CLI and other clients filter by project without needing to set the header.
	if slug := q.Get("project_slug"); slug != "" && r.Header.Get("X-BugBarn-Project") == "" {
		if proj, err := s.projects.Ensure(r.Context(), slug); err == nil {
			r = r.WithContext(storage.WithProjectID(r.Context(), proj.ID))
		}
	}

	filter := domain.IssueFilter{
		Sort:   q.Get("sort"),
		Status: q.Get("status"),
		Query:  q.Get("q"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			filter.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filter.Offset = n
		}
	}
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	requestedLimit := filter.Limit
	filter.Limit = requestedLimit + 1
	knownParams := map[string]bool{"sort": true, "status": true, "q": true, "limit": true, "offset": true, "project_slug": true}
	for key, vals := range q {
		if knownParams[key] || len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
			continue
		}
		if filter.Facets == nil {
			filter.Facets = make(map[string]string)
		}
		filter.Facets[key] = vals[0]
	}
	issues, err := s.issues.ListFiltered(r.Context(), filter)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Ensure non-nil slice so JSON serializes as [] instead of null.
	if issues == nil {
		issues = []domain.Issue{}
	}

	hasMore := len(issues) > requestedLimit
	if hasMore {
		issues = issues[:requestedLimit]
	}
	writeJSON(w, map[string]any{"issues": issues, "hasMore": hasMore})
}

func (s *Server) issueSparklines(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query().Get("ids")
	if ids == "" {
		writeJSON(w, map[string]any{"sparklines": map[string][24]int{}})
		return
	}

	parts := strings.Split(ids, ",")
	issueIDs := make([]int64, 0, len(parts))
	displayByRowID := make(map[int64]string, len(parts))
	for _, p := range parts {
		displayID := strings.TrimSpace(p)
		rowID, err := s.issues.RowIDByDisplayID(r.Context(), displayID)
		if err != nil {
			continue
		}
		issueIDs = append(issueIDs, rowID)
		displayByRowID[rowID] = displayID
	}

	hourlyCounts, err := s.issues.HourlyEventCounts(r.Context(), issueIDs)
	if err != nil {
		hourlyCounts = map[int64][24]int{}
	}

	result := make(map[string][24]int, len(hourlyCounts))
	for id, counts := range hourlyCounts {
		if displayID, ok := displayByRowID[id]; ok {
			result[displayID] = counts
		}
	}
	writeJSON(w, map[string]any{"sparklines": result})
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimPrefix(r.URL.Path, "/api/v1/issues/")
	issue, err := s.issues.Get(r.Context(), issueID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": issue})
}

func (s *Server) resolveIssue(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/resolve")
	issue, queued, err := tryMutate(r.Context(), s.mutQueue,
		mutqueue.Record{Op: mutqueue.OpResolve, IssueID: issueID},
		func(ctx context.Context) (domain.Issue, error) { return s.issues.Resolve(ctx, issueID) },
	)
	if queued {
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"queued": true, "issue_id": issueID})
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": issue})
}

func (s *Server) reopenIssue(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/reopen")
	issue, queued, err := tryMutate(r.Context(), s.mutQueue,
		mutqueue.Record{Op: mutqueue.OpReopen, IssueID: issueID},
		func(ctx context.Context) (domain.Issue, error) { return s.issues.Reopen(ctx, issueID) },
	)
	if queued {
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"queued": true, "issue_id": issueID})
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": issue})
}

func (s *Server) muteIssue(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/mute")
	var body struct {
		MuteMode string `json:"mute_mode"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		http.Error(w, "invalid mute payload", http.StatusBadRequest)
		return
	}
	issue, queued, err := tryMutate(r.Context(), s.mutQueue,
		mutqueue.Record{Op: mutqueue.OpMute, IssueID: issueID, MuteMode: body.MuteMode},
		func(ctx context.Context) (domain.Issue, error) { return s.issues.Mute(ctx, issueID, body.MuteMode) },
	)
	if queued {
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"queued": true, "issue_id": issueID})
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": issue})
}

func (s *Server) unmuteIssue(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/unmute")
	issue, queued, err := tryMutate(r.Context(), s.mutQueue,
		mutqueue.Record{Op: mutqueue.OpUnmute, IssueID: issueID},
		func(ctx context.Context) (domain.Issue, error) { return s.issues.Unmute(ctx, issueID) },
	)
	if queued {
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"queued": true, "issue_id": issueID})
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": issue})
}
