package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const analyticsMaxDays = 366

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

	if end.Sub(start) > analyticsMaxDays*24*time.Hour {
		start = end.AddDate(0, 0, -analyticsMaxDays)
	}

	return analytics.Query{
		ProjectID: projectID,
		Start:     start,
		End:       end,
	}, nil
}

func (s *Server) resolveAnalyticsProjectID(r *http.Request) int64 {
	if slug := r.Header.Get("X-BugBarn-Project"); slug != "" {
		if proj, err := s.projects.Ensure(r.Context(), slug); err == nil {
			return proj.ID
		}
	}
	if id, ok := storage.ProjectIDFromContext(r.Context()); ok && id > 0 {
		return id
	}
	return s.projects.DefaultProjectID()
}

var analyticsRoutes = map[string]func(*Server, http.ResponseWriter, *http.Request, analytics.Query){
	"overview":  (*Server).analyticsOverview,
	"pages":     (*Server).analyticsPages,
	"timeline":  (*Server).analyticsTimeline,
	"referrers": (*Server).analyticsReferrers,
	"segments":  (*Server).analyticsSegments,
	"flow":      (*Server).analyticsFlow,
	"scroll":    (*Server).analyticsScroll,
	"dropout":   (*Server).analyticsDropout,
}

func (s *Server) serveAnalyticsQuery(w http.ResponseWriter, r *http.Request) {
	projectID := s.resolveAnalyticsProjectID(r)

	q, err := parseAnalyticsQuery(r, projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tail := strings.TrimPrefix(r.URL.Path, "/api/v1/analytics/")
	handler, ok := analyticsRoutes[tail]
	if !ok {
		http.NotFound(w, r)
		return
	}
	handler(s, w, r, q)
}

func (s *Server) analyticsOverview(w http.ResponseWriter, r *http.Request, q analytics.Query) {
	result, err := s.analytics.QueryOverview(r.Context(), q)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) analyticsPages(w http.ResponseWriter, r *http.Request, q analytics.Query) {
	pages, err := s.analytics.QueryPages(r.Context(), q)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if pages == nil {
		pages = []analytics.PageStat{}
	}
	writeJSON(w, map[string]any{"pages": pages})
}

func (s *Server) analyticsTimeline(w http.ResponseWriter, r *http.Request, q analytics.Query) {
	granularity := r.URL.Query().Get("granularity")
	switch granularity {
	case "day", "week", "month":
	default:
		granularity = "day"
	}

	buckets, err := s.analytics.QueryTimeline(r.Context(), q, granularity)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, map[string]any{
		"granularity": granularity,
		"buckets":     zeroFillTimeline(buckets, q.Start, q.End, granularity),
	})
}

func (s *Server) analyticsReferrers(w http.ResponseWriter, r *http.Request, q analytics.Query) {
	refs, err := s.analytics.QueryReferrers(r.Context(), q)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if refs == nil {
		refs = []analytics.ReferrerStat{}
	}
	writeJSON(w, map[string]any{"referrers": refs})
}

func (s *Server) analyticsSegments(w http.ResponseWriter, r *http.Request, q analytics.Query) {
	dim := r.URL.Query().Get("dim")
	segs, err := s.analytics.QuerySegments(r.Context(), q, dim)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if segs == nil {
		segs = []analytics.SegmentBucket{}
	}
	writeJSON(w, map[string]any{
		"dim":     dim,
		"buckets": segs,
	})
}

func (s *Server) analyticsFlow(w http.ResponseWriter, r *http.Request, q analytics.Query) {
	pathname := r.URL.Query().Get("pathname")
	result, err := s.analytics.QueryPageFlow(r.Context(), q, pathname)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"pathname": result.Pathname,
		"cameFrom": result.CameFrom,
		"wentTo":   result.WentTo,
	})
}

func (s *Server) analyticsScroll(w http.ResponseWriter, r *http.Request, q analytics.Query) {
	pathname := r.URL.Query().Get("pathname")
	result, err := s.analytics.QueryScrollDepth(r.Context(), q, pathname)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"pathname": result.Pathname,
		"buckets":  result.Buckets,
	})
}

func (s *Server) analyticsDropout(w http.ResponseWriter, r *http.Request, q analytics.Query) {
	stats, err := s.analytics.QueryDropout(r.Context(), q)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"pages": stats})
}

func zeroFillTimeline(buckets []analytics.TimelineBucket, start, end time.Time, granularity string) []analytics.TimelineBucket {
	have := make(map[string]analytics.TimelineBucket, len(buckets))
	for _, b := range buckets {
		have[b.Date] = b
	}

	var keys []string
	switch granularity {
	case "week":
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
	default:
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

func weekStart(t time.Time) time.Time {
	offset := int(t.Weekday())
	return time.Date(t.Year(), t.Month(), t.Day()-offset, 0, 0, 0, 0, time.UTC)
}

func isoWeekNumber(t time.Time) int {
	jan1 := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	dayOfYear := t.YearDay() - 1
	jan1DOW := int(jan1.Weekday())
	return (dayOfYear + jan1DOW) / 7
}

func (s *Server) collectPageView(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var payload struct {
		Pathname         string            `json:"pathname"`
		Hostname         string            `json:"hostname"`
		Referrer         string            `json:"referrer"`
		VisitorID        string            `json:"visitorId"`
		SessionID        string            `json:"sessionId"`
		ScreenWidth      int               `json:"screenWidth"`
		Duration         int64             `json:"duration"`
		MaxScrollPct     int               `json:"maxScrollPct"`
		InteractionCount int               `json:"interactionCount"`
		ExitPathname     string            `json:"exitPathname"`
		Props            map[string]string `json:"props"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(payload.Pathname) == "" {
		http.Error(w, "pathname is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	var projectID int64
	slug := r.Header.Get("X-BugBarn-Project")
	if slug == "" {
		slug = r.URL.Query().Get("project")
	}
	if slug != "" {
		if proj, err := s.projects.Ensure(ctx, slug); err == nil {
			projectID = proj.ID
		}
	}
	if projectID == 0 {
		projectID = s.projects.DefaultProjectID()
	}

	var referrerHost, referrerPath string
	if payload.Referrer != "" {
		if u, err := url.Parse(payload.Referrer); err == nil {
			referrerHost = u.Host
			referrerPath = u.Path
		}
	}

	props := make(map[string]string, len(payload.Props))
	count := 0
	for k, v := range payload.Props {
		if count >= 10 {
			break
		}
		if len(v) > 200 {
			v = v[:200]
		}
		props[k] = v
		count++
	}

	pv := analytics.PageView{
		ProjectID:        projectID,
		Ts:               time.Now().UTC(),
		Pathname:         payload.Pathname,
		Hostname:         payload.Hostname,
		ReferrerHost:     referrerHost,
		ReferrerPath:     referrerPath,
		VisitorID:        payload.VisitorID,
		SessionID:        payload.SessionID,
		DurationMs:       payload.Duration,
		ScreenWidth:      payload.ScreenWidth,
		MaxScrollPct:     payload.MaxScrollPct,
		InteractionCount: payload.InteractionCount,
		ExitPathname:     payload.ExitPathname,
		Props:            props,
	}

	if err := s.analytics.InsertPageView(ctx, pv); err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	writeJSONStatus(w, http.StatusAccepted, map[string]any{})
}

func (s *Server) serveAnalyticsSnippet(w http.ResponseWriter, r *http.Request) {
	origin := s.publicURL
	if origin == "" {
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
			scheme = "http"
		}
		origin = scheme + "://" + r.Host
	}

	project := r.URL.Query().Get("project")

	snippet := `(function(){
  var E="__ENDPOINT__",P="__PROJECT__";
  var vid=localStorage.getItem('_bb_vid');
  if(!vid){vid=(crypto.randomUUID?crypto.randomUUID():Math.random().toString(36).slice(2)+Date.now().toString(36));localStorage.setItem('_bb_vid',vid);}
  var sid=sessionStorage.getItem('_bb_sid');
  if(!sid){sid=(crypto.randomUUID?crypto.randomUUID():Math.random().toString(36).slice(2)+Date.now().toString(36));sessionStorage.setItem('_bb_sid',sid);}
  var t0=Date.now(),scroll=0,clicks=0,exitPath='';
  window.addEventListener('scroll',function(){var s=Math.round(window.scrollY/(document.documentElement.scrollHeight-window.innerHeight||1)*100);if(s>scroll)scroll=s<100?s:100;},{passive:true});
  document.addEventListener('click',function(e){clicks++;var a=e.target.closest('a');if(a&&a.hostname===location.hostname)exitPath=a.pathname;});
  document.addEventListener('keydown',function(){clicks++;});
  function send(dur){
    var payload=JSON.stringify({pathname:location.pathname,hostname:location.hostname,referrer:document.referrer||'',visitorId:vid,sessionId:sid,screenWidth:screen.width,duration:dur,maxScrollPct:scroll,interactionCount:clicks,exitPathname:exitPath,props:(window.__bb_analytics&&window.__bb_analytics.props)||{}});
    navigator.sendBeacon?navigator.sendBeacon(E+'/api/v1/analytics/collect?project='+P,payload):fetch(E+'/api/v1/analytics/collect?project='+P,{method:'POST',body:payload,keepalive:true});
  }
  document.readyState==='loading'?document.addEventListener('DOMContentLoaded',function(){send(0);}):send(0);
  document.addEventListener('visibilitychange',function(){if(document.visibilityState==='hidden')send(Date.now()-t0);});
})();`

	snippet = strings.ReplaceAll(snippet, "__ENDPOINT__", origin)
	snippet = strings.ReplaceAll(snippet, "__PROJECT__", project)

	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(snippet))
}
