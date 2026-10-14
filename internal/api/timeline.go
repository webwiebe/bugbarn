package api

import "net/http"

// serveErrorTimeline answers GET /api/v1/events/timeline?range=24h|7d|30d: the
// error events per project over time, for the stacked chart on the Issues page.
func (s *Server) serveErrorTimeline(w http.ResponseWriter, r *http.Request) {
	result, err := s.timeline.ErrorTimeline(r.Context(), r.URL.Query().Get("range"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, result)
}
