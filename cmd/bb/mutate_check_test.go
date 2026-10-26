package main

import "testing"

func TestRequireIssueStatus(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"muted as asked", `{"issue":{"status":"muted"}}`, "muted", false},
		{"unchanged status", `{"issue":{"status":"unresolved"}}`, "muted", true},
		{"unmute applied", `{"issue":{"status":"unresolved"}}`, "unresolved", false},
		{"unmute not applied", `{"issue":{"status":"muted"}}`, "unresolved", true},
		{"queued mutation", `{"queued":true,"issue_id":"PRO-22"}`, "muted", false},
		{"no issue in response", `{}`, "muted", true},
		{"not json", `oops`, "muted", true},
	}
	for _, tc := range cases {
		err := requireIssueStatus([]byte(tc.body), "PRO-22", tc.want)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}
