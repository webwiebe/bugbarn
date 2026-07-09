package api

import (
	"context"
	"net/http"
)

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

type ingestReport struct {
	Healthy             bool     `json:"healthy"`
	Reasons             []string `json:"reasons,omitempty"`
	LastEventAgeSeconds float64  `json:"lastEventAgeSeconds"`
	HasEvents           bool     `json:"hasEvents"`
	QueueDepth          int64    `json:"queueDepth"`
	QueueDepthKnown     bool     `json:"queueDepthKnown"`
	WALSizeBytes        int64    `json:"walSizeBytes"`
}

type detailedHealth struct {
	Status   string         `json:"status"`
	Worker   *workerReport  `json:"worker,omitempty"`
	Ingest   *ingestReport  `json:"ingest,omitempty"`
	Projects projectsReport `json:"projects"`
}

func (s *Server) serveHealth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("detail") != "true" {
		writeJSON(w, map[string]any{"status": "ok"})
		return
	}

	resp := detailedHealth{Status: "ok"}

	if ir, unhealthy := s.ingestHealthReport(); ir != nil {
		resp.Ingest = ir
		if unhealthy {
			resp.Status = "unhealthy"
		}
	}

	if wr, level := s.workerHealthReport(); wr != nil {
		resp.Worker = wr
		if (level == "degraded" || level == "unhealthy") && resp.Status == "ok" {
			resp.Status = level
		}
	}

	resp.Projects = s.pendingProjectsReport(r.Context())
	if resp.Projects.PendingCount > 0 && resp.Status == "ok" {
		resp.Status = "degraded"
	}

	if resp.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	writeJSON(w, resp)
}

// ingestHealthReport returns the ingest sub-report (nil when unsampled/absent)
// and whether the ingest pipeline is unhealthy.
func (s *Server) ingestHealthReport() (*ingestReport, bool) {
	if s.ingestHealth == nil {
		return nil, false
	}
	snap := s.ingestHealth()
	if !snap.Sampled {
		return nil, false
	}
	return &ingestReport{
		Healthy:             snap.Healthy,
		Reasons:             snap.Reasons,
		LastEventAgeSeconds: snap.LastEventAgeSeconds,
		HasEvents:           snap.HasEvents,
		QueueDepth:          snap.QueueDepth,
		QueueDepthKnown:     snap.QueueDepthKnown,
		WALSizeBytes:        snap.WALSizeBytes,
	}, !snap.Healthy
}

// workerHealthReport returns the worker sub-report (nil when absent) and its
// health level.
func (s *Server) workerHealthReport() (*workerReport, string) {
	if s.workerStatus == nil {
		return nil, ""
	}
	snap := s.workerStatus.Snapshot()
	wr := &workerReport{
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
	return wr, string(snap.Level)
}

// pendingProjectsReport counts projects awaiting admin approval.
func (s *Server) pendingProjectsReport(ctx context.Context) projectsReport {
	var report projectsReport
	if s.projects == nil {
		return report
	}
	projects, err := s.projects.List(ctx)
	if err != nil {
		return report
	}
	for _, p := range projects {
		if p.Status == "pending" {
			report.PendingCount++
			report.PendingSlugs = append(report.PendingSlugs, p.Slug)
		}
	}
	return report
}
