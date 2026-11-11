package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
)

// mutateIssue answers 202 and appends the record when the store is too slow for
// the request, so the admin action survives and is applied later.
func TestMutateIssueQueuesWhenTheStoreIsSlow(t *testing.T) {
	t.Parallel()

	q, err := mutqueue.New(t.TempDir())
	if err != nil {
		t.Fatalf("mutqueue: %v", err)
	}
	defer q.Close()
	srv := &Server{mutQueue: q}

	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues/BS-1/resolve", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.mutateIssue(rr, req, mutqueue.Record{Op: mutqueue.OpResolve, IssueID: "BS-1"},
		func(context.Context) (domain.Issue, error) {
			<-release
			return domain.Issue{}, nil
		})

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rr.Code, rr.Body.String())
	}
	var body struct {
		Queued  bool   `json:"queued"`
		IssueID string `json:"issue_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil || !body.Queued || body.IssueID != "BS-1" {
		t.Fatalf("body = %+v, err %v", body, err)
	}
	var got []mutqueue.Record
	if err := q.Drain(func(r mutqueue.Record) error { got = append(got, r); return nil }); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(got) != 1 || got[0].Op != mutqueue.OpResolve || got[0].IssueID != "BS-1" {
		t.Fatalf("queued records = %+v, want one resolve for BS-1", got)
	}
}

func TestMutateIssueMapsServiceErrors(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/issues/BS-9/reopen", nil)
	rr := httptest.NewRecorder()
	srv.mutateIssue(rr, req, mutqueue.Record{Op: mutqueue.OpReopen, IssueID: "BS-9"},
		func(context.Context) (domain.Issue, error) {
			return domain.Issue{}, apperr.NotFound("issue not found", nil)
		})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
