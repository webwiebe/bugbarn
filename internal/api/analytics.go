package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const analyticsMaxDays = 366

// parseAnalyticsQuery parses common analytics query parameters from the request.
// Defaults to the last 30 days. Clamps to max 366 days. Returns 400 if start > end.
func parseAnalyticsQuery(r *http.Request, projectID int64) (analytics.Query, error) {
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -30)

	if s := r.URL.Query().Get("start"); s != "" {
		t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
		if err != nil {
			return analytics.Query{}, fmt.Errorf("invalid start date: %w", err)
		}
		start = t
	}
	if e := r.URL.Query().Get("end"); e != "" {
		t, err := time.ParseInLocation("2006-01-02", e, time.UTC)
		if err != nil {
			return analytics.Query{}, fmt.Errorf("invalid end date: %w", err)
		}
		end = t
	}

	if start.After(end) {
		return analytics.Query{}, fmt.Errorf("start must not be after end")
	}

	// Clamp to max 366 days.
	if end.Sub(start) > analyticsMaxDays*24*time.Hour {
		start = end.AddDate(0, 0, -analyticsMaxDays)
	}

	return analytics.Query{
		ProjectID: projectID,
		Start:     start,
		End:       end,
	}, nil
}

// resolveAnalyticsProjectID resolves the project ID for analytics requests using
// the same logic as other protected routes.
func (s *Server) resolveAnalyticsProjectID(r *http.Request) int64 {
	if slug := r.Header.Get("X-BugBarn-Project"); slug != "" && s.store != nil {
		if proj, err := s.store.EnsureProject(r.Context(), slug); err == nil {
			return proj.ID
		}
	}
	if id, ok := storage.ProjectIDFromContext(r.Context()); ok && id > 0 {
		return id
	}
	return s.store.DefaultProjectID()
}

// serveAnalyticsQuery handles all GET /api/v1/analytics/* endpoints.
func (s *Server) serveAnalyticsQuery(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}

	projectID := s.resolveAnalyticsProjectID(r)

	q, err := parseAnalyticsQuery(r, projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tail := strings.TrimPrefix(r.URL.Path, "/api/v1/analytics/")

	ctx := r.Context()

	switch tail {
	case "overview":
		result, err := s.store.QueryOverview(ctx, q)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"pageviews":     result.Pageviews,
			"sessions":      result.Sessions,
			"pages":         result.PagesCount,
			"avgDurationMs": result.AvgDurationMs,
		})

	case "pages":
		pages, err := s.store.QueryPages(ctx, q)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		if pages == nil {
			pages = []analytics.PageStat{}
		}
		writeJSON(w, map[string]any{"pages": pages})

	case "timeline":
		granularity := r.URL.Query().Get("granularity")
		if granularity == "" {
			granularity = "day"
		}
		switch granularity {
		case "day", "week", "month":
			// valid
		default:
			granularity = "day"
		}

		buckets, err := s.store.QueryTimeline(ctx, q, granularity)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}

		buckets = zeroFillTimeline(buckets, q.Start, q.End, granularity)

		writeJSON(w, map[string]any{
			"granularity": granularity,
			"buckets":     buckets,
		})

	case "referrers":
		refs, err := s.store.QueryReferrers(ctx, q)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		if refs == nil {
			refs = []analytics.ReferrerStat{}
		}
		writeJSON(w, map[string]any{"referrers": refs})

	case "segments":
		dim := r.URL.Query().Get("dim")
		segs, err := s.store.QuerySegments(ctx, q, dim)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		if segs == nil {
			segs = []analytics.SegmentBucket{}
		}
		writeJSON(w, map[string]any{
			"dim":     dim,
			"buckets": segs,
		})

	default:
		http.NotFound(w, r)
	}
}

// zeroFillTimeline inserts zero-value buckets for any dates missing from the
// DB results so the client always gets a contiguous series.
func zeroFillTimeline(buckets []analytics.TimelineBucket, start, end time.Time, granularity string) []analytics.TimelineBucket {
	// Build a lookup from the DB results.
	have := make(map[string]analytics.TimelineBucket, len(buckets))
	for _, b := range buckets {
		have[b.Date] = b
	}

	var keys []string
	switch granularity {
	case "week":
		// Iterate by week from start to end.
		cur := weekStart(start)
		for !cur.After(end) {
			key := cur.UTC().Format("2006-W") + fmt.Sprintf("%02d", isoWeekNumber(cur))
			keys = append(keys, key)
			cur = cur.AddDate(0, 0, 7)
		}
	case "month":
		cur := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		endMonth := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
		for !cur.After(endMonth) {
			keys = append(keys, cur.Format("2006-01"))
			cur = cur.AddDate(0, 1, 0)
		}
	default: // day
		cur := start
		for !cur.After(end) {
			keys = append(keys, cur.Format("2006-01-02"))
			cur = cur.AddDate(0, 0, 1)
		}
	}

	out := make([]analytics.TimelineBucket, 0, len(keys))
	for _, key := range keys {
		if b, ok := have[key]; ok {
			out = append(out, b)
		} else {
			out = append(out, analytics.TimelineBucket{Date: key})
		}
	}
	return out
}

// weekStart returns the Monday of the week containing t (matching strftime %W
// which counts weeks starting on Sunday, but we align on Monday for ISO feel).
// We use Go's time.Weekday with Sunday=0, Monday=1, …
// strftime('%Y-W%W', date) in SQLite uses Sunday as the first day of the week.
// So we align our iteration to match SQLite's Sunday-based week numbers.
func weekStart(t time.Time) time.Time {
	// Go's time.Sunday == 0, time.Monday == 1, …
	offset := int(t.Weekday()) // 0 for Sunday
	return time.Date(t.Year(), t.Month(), t.Day()-offset, 0, 0, 0, 0, time.UTC)
}

// isoWeekNumber returns the SQLite strftime('%W', …) week number (0-53),
// where weeks start on Sunday.
func isoWeekNumber(t time.Time) int {
	// Jan 1 of the year
	jan1 := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	// Day-of-year (0-based)
	dayOfYear := t.YearDay() - 1
	// Day of week of Jan 1 (Sunday=0)
	jan1DOW := int(jan1.Weekday())
	// SQLite %W: number of Sundays before this date / week of year
	return (dayOfYear + jan1DOW) / 7
}
