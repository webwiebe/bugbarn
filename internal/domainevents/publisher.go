package domainevents

import "github.com/wiebe-xyz/bugbarn/internal/domain"

// IssuePublisher publishes issue events on a Bus after an event is persisted.
type IssuePublisher struct {
	bus *Bus
}

// NewIssuePublisher returns a publisher for bus. A nil bus makes every
// publish a no-op.
func NewIssuePublisher(bus *Bus) *IssuePublisher {
	return &IssuePublisher{bus: bus}
}

// PublishIssueEvent publishes IssueCreated and IssueRegressed when they apply,
// followed by IssueEventRecorded for every persisted event.
func (p *IssuePublisher) PublishIssueEvent(issue domain.Issue, projectID int64, isNew bool, isRegressed bool) {
	if p.bus == nil {
		return
	}
	if isNew {
		p.bus.Publish(IssueCreated{Issue: issue, ProjectID: projectID})
	}
	if isRegressed {
		p.bus.Publish(IssueRegressed{Issue: issue, ProjectID: projectID})
	}
	p.bus.Publish(IssueEventRecorded{Issue: issue, ProjectID: projectID})
}
