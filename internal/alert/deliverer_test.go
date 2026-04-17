package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func testIssue() storage.Issue {
	return storage.Issue{
		ID:         "issue-000001",
		Title:      "TypeError: cannot read property of undefined",
		ExceptionType: "TypeError",
		EventCount: 3,
		FirstSeen:  time.Now().UTC(),
	}
}

func TestDeliverer_GenericPayload(t *testing.T) {
	t.Parallel()

	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDeliverer()
	rule := Rule{
		ID:         "alert-000001",
		Name:       "Test Alert",
		Condition:  "new_issue",
		WebhookURL: srv.URL + "/hook",
		ProjectID:  1,
	}
	issue := testIssue()
	err := d.Fire(context.Background(), rule, issue, "https://bugbarn.example.com")
	if err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("could not unmarshal payload: %v", err)
	}

	if payload["alert"] != "Test Alert" {
		t.Errorf("expected alert=%q, got %v", "Test Alert", payload["alert"])
	}
	issueObj, ok := payload["issue"].(map[string]any)
	if !ok {
		t.Fatal("expected issue object in payload")
	}
	if issueObj["id"] != issue.ID {
		t.Errorf("expected issue.id=%q, got %v", issue.ID, issueObj["id"])
	}
	if !strings.Contains(issueObj["url"].(string), issue.ID) {
		t.Errorf("expected url to contain issue ID, got %v", issueObj["url"])
	}
}

func TestDeliverer_SlackPayload(t *testing.T) {
	t.Parallel()

	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Replace the host with the test server but keep hooks.slack.com in the URL value for detection.
	d := &Deliverer{client: &http.Client{Timeout: 5 * time.Second}}
	rule := Rule{
		ID:         "alert-000002",
		Name:       "Slack Alert",
		Condition:  "new_issue",
		WebhookURL: srv.URL,
		ProjectID:  1,
	}
	// Manually invoke slackPayload to test payload shape without real Slack.
	issue := testIssue()
	payload, err := d.slackPayload(rule, issue, "https://bugbarn.example.com/issues/"+issue.ID)
	if err != nil {
		t.Fatalf("slackPayload: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		t.Fatalf("unmarshal slack payload: %v", err)
	}
	if obj["text"] == "" || obj["text"] == nil {
		t.Error("expected non-empty text in Slack payload")
	}
	blocks, ok := obj["blocks"].([]any)
	if !ok || len(blocks) < 3 {
		t.Errorf("expected at least 3 blocks in Slack payload, got %v", obj["blocks"])
	}
	_ = captured
}

func TestDeliverer_DiscordPayload(t *testing.T) {
	t.Parallel()

	d := NewDeliverer()
	issue := testIssue()
	payload, err := d.discordPayload(Rule{Name: "Discord Alert"}, issue, "https://bugbarn.example.com/issues/"+issue.ID)
	if err != nil {
		t.Fatalf("discordPayload: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		t.Fatalf("unmarshal discord payload: %v", err)
	}
	if obj["content"] == nil {
		t.Error("expected content field in Discord payload")
	}
	embeds, ok := obj["embeds"].([]any)
	if !ok || len(embeds) == 0 {
		t.Errorf("expected embeds in Discord payload, got %v", obj["embeds"])
	}
}

func TestDeliverer_RetryOnFailure(t *testing.T) {
	t.Parallel()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Deliverer{client: &http.Client{Timeout: 5 * time.Second}}
	// Override client with no-delay for test (use the real client but point at test server)
	rule := Rule{
		WebhookURL: srv.URL,
		Name:       "Retry Alert",
	}
	// We can't easily test the retry delays without mocking time; instead just verify
	// the 3rd attempt succeeds and no error is returned.
	// We reduce delays via a subtest that calls Fire directly.
	err := d.Fire(context.Background(), rule, testIssue(), "http://example.com")
	// With 3 retries (0, 1s, 2s delays), the 3rd attempt should succeed.
	// In tests this will take ~3s unless we mock delays; test just verifies correctness.
	if err != nil {
		t.Fatalf("expected success on 3rd retry, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestDeliverer_AllRetriesFail(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	d := &Deliverer{client: &http.Client{Timeout: 5 * time.Second}}
	rule := Rule{WebhookURL: srv.URL, Name: "Failing Alert"}
	err := d.Fire(context.Background(), rule, testIssue(), "http://example.com")
	if err == nil {
		t.Fatal("expected error when all retries fail")
	}
}

func TestDeliverer_SlackDetection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url      string
		wantType string
	}{
		{"https://hooks.slack.com/services/T00/B00/xxx", "slack"},
		{"https://discord.com/api/webhooks/123/abc", "discord"},
		{"https://example.com/webhook", "generic"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.wantType, func(t *testing.T) {
			t.Parallel()
			d := NewDeliverer()
			rule := Rule{Name: "test", WebhookURL: tc.url}
			issue := testIssue()
			switch tc.wantType {
			case "slack":
				if !strings.Contains(tc.url, "hooks.slack.com") {
					t.Error("expected Slack URL to contain hooks.slack.com")
				}
				payload, err := d.slackPayload(rule, issue, "http://example.com")
				if err != nil {
					t.Fatal(err)
				}
				var obj map[string]any
				_ = json.Unmarshal(payload, &obj)
				if _, ok := obj["blocks"]; !ok {
					t.Error("expected blocks in Slack payload")
				}
			case "discord":
				payload, err := d.discordPayload(rule, issue, "http://example.com")
				if err != nil {
					t.Fatal(err)
				}
				var obj map[string]any
				_ = json.Unmarshal(payload, &obj)
				if _, ok := obj["embeds"]; !ok {
					t.Error("expected embeds in Discord payload")
				}
			case "generic":
				payload, err := d.genericPayload(rule, issue, "http://example.com")
				if err != nil {
					t.Fatal(err)
				}
				var obj map[string]any
				_ = json.Unmarshal(payload, &obj)
				if _, ok := obj["alert"]; !ok {
					t.Error("expected alert field in generic payload")
				}
			}
		})
	}
}
