package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

// collectPageView handles POST /api/v1/analytics/collect.
// It is public and unauthenticated; CORS headers are set by the caller in ServeHTTP.
func (s *Server) collectPageView(w http.ResponseWriter, r *http.Request) {
	// Accept application/json and text/plain (navigator.sendBeacon sends text/plain).
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var payload struct {
		Pathname    string            `json:"pathname"`
		Hostname    string            `json:"hostname"`
		Referrer    string            `json:"referrer"`
		SessionID   string            `json:"sessionId"`
		ScreenWidth int               `json:"screenWidth"`
		Duration    int64             `json:"duration"`
		Props       map[string]string `json:"props"`
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

	// Resolve project ID from X-BugBarn-Project header or ?project= query param.
	var projectID int64
	slug := r.Header.Get("X-BugBarn-Project")
	if slug == "" {
		slug = r.URL.Query().Get("project")
	}
	if slug != "" && s.store != nil {
		if proj, err := s.store.EnsureProject(ctx, slug); err == nil {
			projectID = proj.ID
		}
	}
	if projectID == 0 && s.store != nil {
		projectID = s.store.DefaultProjectID()
	}

	// Parse referrer URL — split into host + path; strip query string.
	var referrerHost, referrerPath string
	if payload.Referrer != "" {
		if u, err := url.Parse(payload.Referrer); err == nil {
			referrerHost = u.Host
			referrerPath = u.Path // Path already excludes query string
		}
	}

	// Enforce props sanity: max 10 keys, values truncated to 200 chars.
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
		ProjectID:    projectID,
		Ts:           time.Now().UTC(),
		Pathname:     payload.Pathname,
		Hostname:     payload.Hostname,
		ReferrerHost: referrerHost,
		ReferrerPath: referrerPath,
		SessionID:    payload.SessionID,
		DurationMs:   payload.Duration,
		ScreenWidth:  payload.ScreenWidth,
		Props:        props,
	}

	if s.store != nil {
		if err := s.store.InsertPageView(ctx, pv); err != nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
	}

	writeJSONStatus(w, http.StatusAccepted, map[string]any{})
}

// serveAnalyticsSnippet handles GET /analytics.js.
// Returns the tracking snippet with endpoint and project injected.
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
  var sid=sessionStorage.getItem('_bb_sid');
  if(!sid){sid=(crypto.randomUUID?crypto.randomUUID():Math.random().toString(36).slice(2)+Date.now().toString(36));sessionStorage.setItem('_bb_sid',sid);}
  var t0=Date.now();
  function send(dur){
    var payload=JSON.stringify({pathname:location.pathname,hostname:location.hostname,referrer:document.referrer||'',sessionId:sid,screenWidth:screen.width,duration:dur,props:(window.__bb_analytics&&window.__bb_analytics.props)||{}});
    navigator.sendBeacon?navigator.sendBeacon(E+'/api/v1/analytics/collect?project='+P,payload):fetch(E+'/api/v1/analytics/collect?project='+P,{method:'POST',body:payload,keepalive:true});
  }
  document.readyState==='loading'?document.addEventListener('DOMContentLoaded',function(){send(0);}):send(0);
  document.addEventListener('visibilitychange',function(){if(document.visibilityState==='hidden')send(Date.now()-t0);});
})();`

	snippet = strings.ReplaceAll(snippet, "__ENDPOINT__", origin)
	snippet = strings.ReplaceAll(snippet, "__PROJECT__", project)

	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(snippet))
}
