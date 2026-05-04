package service

import (
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
)

// EventPublisher publishes domain events after successful event persistence.
type EventPublisher struct {
	bus *domainevents.Bus
}

func NewEventPublisher(bus *domainevents.Bus) *EventPublisher {
	return &EventPublisher{bus: bus}
}

func (p *EventPublisher) PublishIssueEvent(issue domain.Issue, projectID int64, isNew bool, isRegressed bool) {
	if p.bus == nil {
		return
	}
	if isNew {
		p.bus.Publish(domainevents.IssueCreated{Issue: issue, ProjectID: projectID})
	}
	if isRegressed {
		p.bus.Publish(domainevents.IssueRegressed{Issue: issue, ProjectID: projectID})
	}
	p.bus.Publish(domainevents.IssueEventRecorded{Issue: issue, ProjectID: projectID})
}
