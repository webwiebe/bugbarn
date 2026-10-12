package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// issuePagesServer mimics GET /api/v1/issues: default limit 100, maximum 500,
// hasMore when rows remain past the requested page.
type issuePagesServer struct {
	*httptest.Server
	mu      sync.Mutex
	queries []map[string]string
}

func newIssuePagesServer(t *testing.T, total int) *issuePagesServer {
	t.Helper()
	s := &issuePagesServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seen := map[string]string{}
		for k := range q {
			seen[k] = q.Get(k)
		}
		s.mu.Lock()
		s.queries = append(s.queries, seen)
		s.mu.Unlock()

		limit := 100
		if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 500 {
			limit = n
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		end := offset + limit
		hasMore := end < total
		if end > total {
			end = total
		}
		issues := []map[string]int{}
		for i := offset; i < end; i++ {
			issues = append(issues, map[string]int{"n": i})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issues": issues, "hasMore": hasMore})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *issuePagesServer) client() *Client {
	return &Client{base: s.URL, http: s.Client()}
}

func (s *issuePagesServer) requests() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), s.queries...)
}

type issuePage struct {
	Issues  []map[string]int `json:"issues"`
	HasMore bool             `json:"hasMore"`
}

func decodeIssuePage(t *testing.T, raw json.RawMessage) issuePage {
	t.Helper()
	var p issuePage
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return p
}

func TestFetchIssuesAllReturnsEveryIssue(t *testing.T) {
	t.Parallel()

	srv := newIssuePagesServer(t, 1201)
	res, err := fetchIssues(srv.client(), "status=open", issuePaging{all: true})
	if err != nil {
		t.Fatal(err)
	}

	page := decodeIssuePage(t, res.body)
	if len(page.Issues) != 1201 {
		t.Fatalf("issues = %d; want 1201", len(page.Issues))
	}
	for i, issue := range page.Issues {
		if issue["n"] != i {
			t.Fatalf("issue %d has n=%d; pages overlap or skip", i, issue["n"])
		}
	}
	if page.HasMore {
		t.Fatal("hasMore = true after following every page")
	}
	if res.hint != "" {
		t.Fatalf("hint = %q; want none when everything was fetched", res.hint)
	}
}

func TestFetchIssuesAllHonoursStartingOffset(t *testing.T) {
	t.Parallel()

	srv := newIssuePagesServer(t, 1201)
	res, err := fetchIssues(srv.client(), "status=open", issuePaging{all: true, offset: 1000, offsetSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(decodeIssuePage(t, res.body).Issues); got != 201 {
		t.Fatalf("issues = %d; want 201", got)
	}
}

func TestFetchIssuesAllStopsAtTheCap(t *testing.T) {
	t.Parallel()

	srv := newIssuePagesServer(t, 1201)
	res, err := fetchIssues(srv.client(), "status=open", issuePaging{all: true, maxIssues: 600})
	if err != nil {
		t.Fatal(err)
	}

	page := decodeIssuePage(t, res.body)
	if len(page.Issues) != 600 {
		t.Fatalf("issues = %d; want the 600 cap", len(page.Issues))
	}
	if !page.HasMore {
		t.Fatal("hasMore = false although the cap cut the list short")
	}
	if res.hint == "" {
		t.Fatal("no hint although the cap cut the list short")
	}
}

func TestFetchIssuesPassesLimitAndOffset(t *testing.T) {
	t.Parallel()

	srv := newIssuePagesServer(t, 1201)
	res, err := fetchIssues(srv.client(), "status=open&sort=last_seen", issuePaging{limit: 250, offset: 300, offsetSet: true})
	if err != nil {
		t.Fatal(err)
	}

	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d; want 1", len(reqs))
	}
	if reqs[0]["limit"] != "250" || reqs[0]["offset"] != "300" {
		t.Fatalf("query = %v; want limit=250 offset=300", reqs[0])
	}
	if reqs[0]["status"] != "open" || reqs[0]["sort"] != "last_seen" {
		t.Fatalf("query = %v; the existing filters were lost", reqs[0])
	}

	page := decodeIssuePage(t, res.body)
	if len(page.Issues) != 250 || page.Issues[0]["n"] != 300 {
		t.Fatalf("got %d issues starting at %d; want 250 starting at 300", len(page.Issues), page.Issues[0]["n"])
	}
}

func TestFetchIssuesHintsWhenTruncated(t *testing.T) {
	t.Parallel()

	srv := newIssuePagesServer(t, 1201)
	res, err := fetchIssues(srv.client(), "status=open", issuePaging{})
	if err != nil {
		t.Fatal(err)
	}

	want := "more results: use --offset 100 or --all"
	if res.hint != want {
		t.Fatalf("hint = %q; want %q", res.hint, want)
	}
	reqs := srv.requests()
	if _, ok := reqs[0]["limit"]; ok {
		t.Fatalf("query = %v; limit must stay unset so the server default applies", reqs[0])
	}
}

func TestFetchIssuesHintCountsFromOffsetAndLimit(t *testing.T) {
	t.Parallel()

	srv := newIssuePagesServer(t, 1201)
	res, err := fetchIssues(srv.client(), "status=open", issuePaging{limit: 200, offset: 300, offsetSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := "more results: use --offset 500 or --all"; res.hint != want {
		t.Fatalf("hint = %q; want %q", res.hint, want)
	}
}

func TestFetchIssuesNoHintOnTheLastPage(t *testing.T) {
	t.Parallel()

	srv := newIssuePagesServer(t, 40)
	res, err := fetchIssues(srv.client(), "status=open", issuePaging{})
	if err != nil {
		t.Fatal(err)
	}
	if res.hint != "" {
		t.Fatalf("hint = %q; want none when hasMore is false", res.hint)
	}
}

func TestIssuePagingValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		paging  issuePaging
		wantErr bool
	}{
		{"defaults", issuePaging{}, false},
		{"limit at bounds", issuePaging{limit: 500}, false},
		{"limit too high", issuePaging{limit: 501}, true},
		{"limit negative", issuePaging{limit: -1}, true},
		{"offset negative", issuePaging{offset: -5, offsetSet: true}, true},
		{"all with limit", issuePaging{all: true, limit: 50}, false},
	}
	for _, tc := range cases {
		err := tc.paging.validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v; wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}
