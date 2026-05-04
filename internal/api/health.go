package api

import "net/http"

func (s *Server) serveHealth(w http.ResponseWriter, r *http.Request) {
	detail := r.URL.Query().Get("detail") == "true"

	if !detail {
		writeJSON(w, map[string]any{"status": "ok"})
		return
	}

	type workerReport struct {
		Healthy         bool    `json:"healthy"`
		Level           string  `json:"level"`
		LastAdvance     *string `json:"lastAdvance"`
		PendingRecords  int64   `json:"pendingRecords"`
		DeadLetterCount int64   `json:"deadLetterCount"`
		ProcessedTotal  int64   `json:"processedTotal"`
		StaleSince      *string `json:"staleSince"`
	}

	type projectsReport struct {
		PendingCount int      `json:"pendingCount"`
		PendingSlugs []string `json:"pendingSlugs,omitempty"`
	}

	type detailedHealth struct {
		Status   string         `json:"status"`
		Worker   *workerReport  `json:"worker,omitempty"`
		Projects projectsReport `json:"projects"`
	}

	resp := detailedHealth{Status: "ok"}

	if s.workerStatus != nil {
		snap := s.workerStatus.Snapshot()
		wr := workerReport{
			Healthy:         snap.Healthy,
			Level:           string(snap.Level),
			PendingRecords:  snap.PendingRecords,
			DeadLetterCount: snap.DeadLetterCount,
			ProcessedTotal:  snap.ProcessedTotal,
		}
		if snap.LastAdvance != nil {
			t := snap.LastAdvance.Format("2006-01-02T15:04:05Z")
			wr.LastAdvance = &t
		}
		if snap.StaleSince != nil {
			t := snap.StaleSince.Format("2006-01-02T15:04:05Z")
			wr.StaleSince = &t
		}
		resp.Worker = &wr

		if snap.Level == "degraded" || snap.Level == "unhealthy" {
			if resp.Status == "ok" {
				resp.Status = string(snap.Level)
			}
		}
	}

	if s.projects != nil {
		projects, err := s.projects.List(r.Context())
		if err == nil {
			for _, p := range projects {
				if p.Status == "pending" {
					resp.Projects.PendingCount++
					resp.Projects.PendingSlugs = append(resp.Projects.PendingSlugs, p.Slug)
				}
			}
		}
		if resp.Projects.PendingCount > 0 && resp.Status == "ok" {
			resp.Status = "degraded"
		}
	}

	if resp.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	writeJSON(w, resp)
}
