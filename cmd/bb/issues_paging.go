package main

import (
	"encoding/json"
	"fmt"
	"net/url"
)

const (
	issuesDefaultPageSize = 100 // the API's page size when limit is absent
	issuesMaxPageSize     = 500 // the API rejects a larger limit
	issuesAllCap          = 20000
)

// issuePaging carries the paging flags of `bb issues`.
type issuePaging struct {
	limit     int // 0 leaves the API default in place
	offset    int
	offsetSet bool
	all       bool
	maxIssues int // cap for --all; 0 means issuesAllCap
}

// issuesResult is the body to print plus an optional stderr hint.
type issuesResult struct {
	body json.RawMessage
	hint string
}

func (p issuePaging) validate() error {
	if p.limit < 0 || p.limit > issuesMaxPageSize {
		return fmt.Errorf("--limit must be between 1 and %d", issuesMaxPageSize)
	}
	if p.offset < 0 {
		return fmt.Errorf("--offset must be 0 or higher")
	}
	return nil
}

type issuesPage struct {
	Issues  []json.RawMessage `json:"issues"`
	HasMore bool              `json:"hasMore"`
}

// fetchIssues lists issues for the given filter query. Without --all it makes
// one request and hints on stderr when the API reports more results. With --all
// it follows hasMore page by page and returns one merged list.
func fetchIssues(client *Client, query string, p issuePaging) (issuesResult, error) {
	if p.all {
		return fetchAllIssues(client, query, p)
	}

	data, err := client.get("/api/v1/issues?" + pagedQuery(query, p.limit, p.offset, p.limit > 0, p.offsetSet))
	if err != nil {
		return issuesResult{}, err
	}

	var page issuesPage
	if json.Unmarshal(data, &page) != nil || !page.HasMore {
		return issuesResult{body: data}, nil
	}
	size := p.limit
	if size == 0 {
		size = issuesDefaultPageSize
	}
	return issuesResult{
		body: data,
		hint: fmt.Sprintf("more results: use --offset %d or --all", p.offset+size),
	}, nil
}

func fetchAllIssues(client *Client, query string, p issuePaging) (issuesResult, error) {
	pageSize := p.limit
	if pageSize == 0 {
		pageSize = issuesMaxPageSize
	}
	maxIssues := p.maxIssues
	if maxIssues == 0 {
		maxIssues = issuesAllCap
	}

	all, hasMore, err := collectIssuePages(client, query, pageSize, p.offset, maxIssues)
	if err != nil {
		return issuesResult{}, err
	}

	hint := ""
	if hasMore && len(all) >= maxIssues {
		all = all[:maxIssues]
		hint = fmt.Sprintf("stopped at the %d issue cap: continue with --offset %d", maxIssues, p.offset+maxIssues)
	}

	body, err := json.Marshal(map[string]any{"issues": nonNil(all), "hasMore": hasMore})
	if err != nil {
		return issuesResult{}, fmt.Errorf("encode issues: %w", err)
	}
	return issuesResult{body: body, hint: hint}, nil
}

// collectIssuePages requests pages from offset until the API reports no more
// results or maxIssues have been gathered. It may return a few more than
// maxIssues (the last page is kept whole); the caller trims.
func collectIssuePages(client *Client, query string, pageSize, offset, maxIssues int) ([]json.RawMessage, bool, error) {
	var all []json.RawMessage
	for {
		data, err := client.get("/api/v1/issues?" + pagedQuery(query, pageSize, offset, true, true))
		if err != nil {
			return nil, false, err
		}
		var page issuesPage
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, false, fmt.Errorf("decode issues page: %w", err)
		}
		all = append(all, page.Issues...)
		if !page.HasMore || len(page.Issues) == 0 || len(all) >= maxIssues {
			return all, page.HasMore, nil
		}
		offset += len(page.Issues)
	}
}

func pagedQuery(query string, limit, offset int, withLimit, withOffset bool) string {
	params, err := url.ParseQuery(query)
	if err != nil {
		params = url.Values{}
	}
	if withLimit {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if withOffset {
		params.Set("offset", fmt.Sprintf("%d", offset))
	}
	return params.Encode()
}

func nonNil(v []json.RawMessage) []json.RawMessage {
	if v == nil {
		return []json.RawMessage{}
	}
	return v
}
