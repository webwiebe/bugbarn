package cfpoller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is Cloudflare's GraphQL Analytics API.
const DefaultEndpoint = "https://api.cloudflare.com/client/v4/graphql"

// eventFields are the firewallEventsAdaptive fields requested per event. The
// names match what secnorm's cloudflare normalizer reads; rayName and
// userAgent only end up in the raw JSON.
const eventFields = `action clientIP clientCountryName clientRequestHTTPHost
clientRequestHTTPMethodName clientRequestPath datetime edgeResponseStatus
rayName ruleId source userAgent`

const queryTemplate = `query FirewallEvents($zoneTag: string, $filter: ZoneFirewallEventsAdaptiveFilter_InputObject) {
  viewer {
    zones(filter: {zoneTag: $zoneTag}) {
      firewallEventsAdaptive(filter: $filter, limit: %d, orderBy: [datetime_ASC]) {
        %s
      }
    }
  }
}`

// maxResponseBytes bounds one GraphQL response body.
const maxResponseBytes = 64 << 20

// rateLimitError marks an HTTP 429; retryAfter carries the Retry-After hint.
type rateLimitError struct{ retryAfter time.Duration }

func (e *rateLimitError) Error() string { return "cloudflare rate limit (HTTP 429)" }

type gqlResponse struct {
	Data struct {
		Viewer struct {
			Zones []struct {
				Events []map[string]any `json:"firewallEventsAdaptive"`
			} `json:"zones"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// fetch returns up to limit firewall events of one zone with
// since <= datetime <= until, oldest first.
func (p *Poller) fetch(ctx context.Context, zone string, since, until time.Time, limit int) ([]map[string]any, error) {
	body, err := json.Marshal(map[string]any{
		"query": fmt.Sprintf(queryTemplate, limit, eventFields),
		"variables": map[string]any{
			"zoneTag": zone,
			"filter": map[string]string{
				"datetime_geq": since.UTC().Format(time.RFC3339),
				"datetime_leq": until.UTC().Format(time.RFC3339),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeResponse(resp)
}

func decodeResponse(resp *http.Response) ([]map[string]any, error) {
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &rateLimitError{retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cloudflare graphql: HTTP %d", resp.StatusCode)
	}
	var out gqlResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode cloudflare graphql response: %w", err)
	}
	if len(out.Errors) > 0 {
		msgs := make([]string, 0, len(out.Errors))
		for _, e := range out.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, errors.New("cloudflare graphql: " + strings.Join(msgs, "; "))
	}
	if len(out.Data.Viewer.Zones) == 0 {
		return nil, errors.New("cloudflare graphql: zone not found or token lacks Analytics Read on it")
	}
	return out.Data.Viewer.Zones[0].Events, nil
}

func parseRetryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}
