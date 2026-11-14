package worker

import (
	"testing"
	"time"
)

func TestStatusHealthLevels(t *testing.T) {
	t.Parallel()
	s := NewStatus()
	if r := s.Snapshot(); r.Level != HealthOK || !r.Healthy || r.PendingRecords != 0 {
		t.Fatalf("fresh status = %+v, want ok", r)
	}

	s.SetPendingRecords(3)
	s.RecordProcessed(2)
	s.RecordProcessed(5)
	s.RecordDeadLetter()
	s.RecordAdvance()
	r := s.Snapshot()
	if r.Level != HealthOK || r.PendingRecords != 3 || r.ProcessedTotal != 7 || r.DeadLetterCount != 1 || r.LastAdvance == nil {
		t.Errorf("recent advance with a backlog = %+v", r)
	}

	s.mu.Lock()
	s.lastAdvance = s.lastAdvance.Add(-6 * time.Minute)
	s.mu.Unlock()
	if r := s.Snapshot(); r.Level != HealthDegraded || r.Healthy || r.StaleSince == nil {
		t.Errorf("6 minutes without an advance = %+v, want degraded", r)
	}

	s.mu.Lock()
	s.lastAdvance = s.lastAdvance.Add(-10 * time.Minute)
	s.mu.Unlock()
	if r := s.Snapshot(); r.Level != HealthUnhealthy || r.Healthy {
		t.Errorf("16 minutes without an advance = %+v, want unhealthy", r)
	}

	s.SetPendingRecords(0)
	if r := s.Snapshot(); r.Level != HealthOK {
		t.Errorf("an empty backlog is ok however old the last advance: %+v", r)
	}
}
