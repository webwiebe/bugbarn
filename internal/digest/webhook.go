package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookNotifier delivers the digest as a JSON POST to a configured URL.
type WebhookNotifier struct {
	URL string
}

func (n *WebhookNotifier) Name() string { return "webhook" }

func (n *WebhookNotifier) Send(ctx context.Context, report Report) error {
	p := struct {
		Type string `json:"type"`
		Report
	}{Type: "weekly_digest", Report: report}

	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}
