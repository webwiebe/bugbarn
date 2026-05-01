package worker

import (
	"sync"
	"sync/atomic"
	"time"
)

type HealthLevel string

const (
	HealthOK        HealthLevel = "ok"
	HealthDegraded  HealthLevel = "degraded"
	HealthUnhealthy HealthLevel = "unhealthy"
)

type Status struct {
	mu              sync.Mutex
	lastAdvance     time.Time
	deadLetterCount int64
	processedTotal  int64
	pendingRecords  atomic.Int64
}

type HealthReport struct {
	Healthy        bool        `json:"healthy"`
	Level          HealthLevel `json:"level"`
	LastAdvance    *time.Time  `json:"lastAdvance"`
	PendingRecords int64      `json:"pendingRecords"`
	DeadLetterCount int64     `json:"deadLetterCount"`
	ProcessedTotal int64      `json:"processedTotal"`
	StaleSince     *time.Time `json:"staleSince"`
}

func (s *Status) RecordAdvance() {
	s.mu.Lock()
	s.lastAdvance = time.Now().UTC()
	s.mu.Unlock()
}

func (s *Status) RecordProcessed(n int64) {
	s.mu.Lock()
	s.processedTotal += n
	s.mu.Unlock()
}

func (s *Status) RecordDeadLetter() {
	s.mu.Lock()
	s.deadLetterCount++
	s.mu.Unlock()
}

func (s *Status) SetPendingRecords(n int64) {
	s.pendingRecords.Store(n)
}

func (s *Status) Snapshot() HealthReport {
	s.mu.Lock()
	lastAdv := s.lastAdvance
	dlCount := s.deadLetterCount
	processed := s.processedTotal
	s.mu.Unlock()

	pending := s.pendingRecords.Load()
	now := time.Now().UTC()

	report := HealthReport{
		PendingRecords:  pending,
		DeadLetterCount: dlCount,
		ProcessedTotal:  processed,
	}

	if !lastAdv.IsZero() {
		t := lastAdv
		report.LastAdvance = &t
	}

	staleDuration := 5 * time.Minute
	unhealthyDuration := 15 * time.Minute

	switch {
	case pending == 0:
		report.Level = HealthOK
		report.Healthy = true
	case lastAdv.IsZero():
		if processed == 0 && pending > 0 {
			report.Level = HealthDegraded
			report.Healthy = false
		} else {
			report.Level = HealthOK
			report.Healthy = true
		}
	case now.Sub(lastAdv) > unhealthyDuration && pending > 0:
		report.Level = HealthUnhealthy
		report.Healthy = false
		stale := lastAdv.Add(staleDuration)
		report.StaleSince = &stale
	case now.Sub(lastAdv) > staleDuration && pending > 0:
		report.Level = HealthDegraded
		report.Healthy = false
		stale := lastAdv.Add(staleDuration)
		report.StaleSince = &stale
	default:
		report.Level = HealthOK
		report.Healthy = true
	}

	return report
}
