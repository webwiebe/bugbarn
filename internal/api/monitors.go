package api

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	monitorsvc "github.com/wiebe-xyz/bugbarn/internal/service/monitors"
)

const (
	monitorsPathPrefix = "/api/v1/monitors/"
	checkInsSuffix     = "/check-ins"
)

// serveMonitorCheckInEndpoint handles POST /api/v1/monitors/<slug>/check-ins.
// A scheduled job calls it with a project key to report "ok" or "error".
//
//	{"status":"ok","message":"...","interval":"1h","grace":"5m"}
//
// interval and grace are optional; sent, they create or update the monitor.
// Readers forward the write to the writer, which owns the database.
func (s *Server) serveMonitorCheckInEndpoint(w http.ResponseWriter, r *http.Request) bool {
	slug, ok := monitorCheckInSlug(r.URL.Path)
	if !ok {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	if s.ingestHandler == nil || !s.ingestHandler.ValidAPIKey(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return true
	}
	if s.writeForwarder != nil {
		s.writeForwarder.Forward(w, r)
		return true
	}
	if s.monitors == nil {
		http.Error(w, "monitors unavailable", http.StatusServiceUnavailable)
		return true
	}
	s.recordCheckIn(w, r, slug)
	return true
}

// monitorCheckInSlug extracts <slug> from /api/v1/monitors/<slug>/check-ins.
func monitorCheckInSlug(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, monitorsPathPrefix)
	if !ok {
		return "", false
	}
	slug, ok := strings.CutSuffix(rest, checkInsSuffix)
	if !ok || slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

type checkInBody struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Interval string `json:"interval"`
	Grace    string `json:"grace"`
}

func (s *Server) recordCheckIn(w http.ResponseWriter, r *http.Request, slug string) {
	projectID, err := s.checkInProject(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var body checkInBody
	if err := decodeJSON(w, r, &body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	in := monitorsvc.CheckInInput{Slug: slug, Status: body.Status, Message: body.Message}
	if in.Interval, err = parseOptionalDuration("interval", body.Interval); err != nil {
		writeServiceError(w, err)
		return
	}
	if in.Grace, err = parseOptionalDuration("grace", body.Grace); err != nil {
		writeServiceError(w, err)
		return
	}
	res, err := s.monitors.CheckIn(r.Context(), projectID, in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSONStatus(w, status, map[string]any{
		"monitor":   res.Monitor.Slug,
		"state":     res.Monitor.State,
		"dueBy":     res.Monitor.NextDue().UTC().Format(time.RFC3339),
		"interval":  res.Monitor.Interval.String(),
		"grace":     res.Monitor.Grace.String(),
		"createdAt": res.Monitor.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// checkInProject resolves the project a check-in belongs to: the project the
// key is bound to, or the X-BugBarn-Project header for an unbound key. It never
// creates a project.
func (s *Server) checkInProject(r *http.Request) (int64, error) {
	pid, _, ok := s.ingestHandler.APIKeyProjectScope(r)
	if ok && pid > 0 {
		return pid, nil
	}
	slug := r.Header.Get("X-BugBarn-Project")
	if slug == "" || s.projects == nil {
		return 0, apperr.InvalidInput("the key is not bound to a project: send X-BugBarn-Project", nil)
	}
	proj, err := s.projects.BySlug(r.Context(), slug)
	if err != nil || !projectIsActive(proj.Status) {
		return 0, apperr.NotFound("unknown or inactive project", err)
	}
	return proj.ID, nil
}

func parseOptionalDuration(field, value string) (*time.Duration, error) {
	if value == "" {
		return nil, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return nil, apperr.InvalidInput(field+` must be a duration such as "1h" or "90s"`, err)
	}
	return &d, nil
}

// listMonitors handles GET /api/v1/monitors: the monitors of the scoped project,
// or of every project in the caller's scope. Monitors that need attention
// (error, missed) come first.
func (s *Server) listMonitors(w http.ResponseWriter, r *http.Request) {
	if s.monitors == nil {
		http.Error(w, "monitors unavailable", http.StatusServiceUnavailable)
		return
	}
	var (
		list []domain.Monitor
		err  error
	)
	if pid, ok := domain.ProjectIDFromContext(r.Context()); ok && pid > 0 {
		list, err = s.monitors.List(r.Context(), pid)
	} else {
		list, err = s.monitors.ListAll(r.Context())
		if ids, scoped := domain.ProjectIDsFromContext(r.Context()); scoped {
			list = slices.DeleteFunc(list, func(m domain.Monitor) bool { return !slices.Contains(ids, m.ProjectID) })
		}
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	sort.SliceStable(list, func(i, j int) bool {
		return monitorRank(list[i].State) < monitorRank(list[j].State)
	})
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		out = append(out, monitorJSON(m))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"monitors": out})
}

func monitorRank(state string) int {
	switch state {
	case domain.MonitorStateError, domain.MonitorStateMissed:
		return 0
	}
	return 1
}

func monitorJSON(m domain.Monitor) map[string]any {
	out := map[string]any{
		"slug":            m.Slug,
		"project":         m.ProjectSlug,
		"state":           m.State,
		"lastStatus":      m.LastStatus,
		"intervalSeconds": int64(m.Interval / time.Second),
		"graceSeconds":    int64(m.Grace / time.Second),
		"dueBy":           m.NextDue().UTC().Format(time.RFC3339),
		"stateSince":      m.StateSince.UTC().Format(time.RFC3339),
	}
	if !m.LastCheckInAt.IsZero() {
		out["lastCheckInAt"] = m.LastCheckInAt.UTC().Format(time.RFC3339)
	}
	return out
}
