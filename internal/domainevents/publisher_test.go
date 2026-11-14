package domainevents

import (
	"reflect"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func collect(b *Bus) *[]any {
	var got []any
	b.Subscribe(func(e any) { got = append(got, e) })
	return &got
}

func TestPublishIssueEventSequences(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "issue-000001"}
	cases := []struct {
		name        string
		isNew       bool
		isRegressed bool
		want        []any
	}{
		{"plain event", false, false, []any{IssueEventRecorded{Issue: issue, ProjectID: 7}}},
		{"new issue", true, false, []any{
			IssueCreated{Issue: issue, ProjectID: 7},
			IssueEventRecorded{Issue: issue, ProjectID: 7},
		}},
		{"regression", false, true, []any{
			IssueRegressed{Issue: issue, ProjectID: 7},
			IssueEventRecorded{Issue: issue, ProjectID: 7},
		}},
		{"new and regressed", true, true, []any{
			IssueCreated{Issue: issue, ProjectID: 7},
			IssueRegressed{Issue: issue, ProjectID: 7},
			IssueEventRecorded{Issue: issue, ProjectID: 7},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var bus Bus
			got := collect(&bus)
			NewIssuePublisher(&bus).PublishIssueEvent(issue, 7, tc.isNew, tc.isRegressed)
			if !reflect.DeepEqual(*got, tc.want) {
				t.Fatalf("published %#v, want %#v", *got, tc.want)
			}
		})
	}
}

func TestPublishIssueEventNilBusIsNoop(t *testing.T) {
	t.Parallel()
	NewIssuePublisher(nil).PublishIssueEvent(domain.Issue{}, 1, true, true)
}
