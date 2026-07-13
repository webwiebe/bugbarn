package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

type Client struct {
	base    string
	http    *http.Client
	config  Config
	project string // project slug → X-BugBarn-Project header
	group   string // group slug → X-BugBarn-Group header (overrides project)
}

func newClient() (*Client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return &Client{
		base:   strings.TrimRight(cfg.URL, "/"),
		http:   &http.Client{Timeout: 30 * time.Second},
		config: cfg,
	}, nil
}

func (c *Client) get(path string) (json.RawMessage, error) {
	return c.do("GET", path, nil)
}

func (c *Client) post(path string, body any) (json.RawMessage, error) {
	return c.do("POST", path, body)
}

func (c *Client) patch(path string, body any) (json.RawMessage, error) {
	return c.do("PATCH", path, body)
}

func (c *Client) do(method, path string, body any) (json.RawMessage, error) {
	return c.doRetry(context.Background(), method, path, body, false)
}

func (c *Client) doRetry(ctx context.Context, method, path string, body any, retried bool) (json.RawMessage, error) {
	target, _, _ := strings.Cut(path, "?")

	ctx, span := tracing.Tracer().Start(ctx, "cli.Request",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.method", method),
			attribute.String("http.target", target),
		),
	)
	defer span.End()

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bodyReader)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	c.setRequestHeaders(req, method, body != nil)

	// Propagate the client span's trace context to the server so this request
	// correlates with the server-side trace (extracted in tracing.Middleware).
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := c.http.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("read response: %w", err)
	}

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	if result, retriedResp, err := c.reauthAndRetry(ctx, method, path, body, retried, resp.StatusCode); retriedResp {
		return result, err
	}

	if resp.StatusCode >= 400 {
		msg := string(respBody)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(msg))
	}

	return json.RawMessage(respBody), nil
}

// setRequestHeaders sets the content-type, project/group scoping, and auth
// headers (API key, session cookie, CSRF token) on an outgoing request.
func (c *Client) setRequestHeaders(req *http.Request, method string, hasBody bool) {
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.group != "" {
		req.Header.Set("X-BugBarn-Group", c.group)
	} else if c.project != "" {
		req.Header.Set("X-BugBarn-Project", c.project)
	}

	switch c.config.Auth.Type {
	case "apikey":
		req.Header.Set("X-BugBarn-API-Key", c.config.Auth.APIKey)
	case "session":
		req.AddCookie(&http.Cookie{Name: "bugbarn_session", Value: c.config.Auth.SessionToken})
		if c.config.Auth.CSRFToken != "" && method != "GET" {
			req.Header.Set("X-BugBarn-CSRF", c.config.Auth.CSRFToken)
		}
	}
}

// reauthAndRetry re-authenticates with the configured username/password and
// replays the request once when a session-authenticated call comes back
// unauthorized. The bool return reports whether it handled (attempted) the
// retry at all; callers should return its result whenever it does.
func (c *Client) reauthAndRetry(
	ctx context.Context, method, path string, body any, retried bool, statusCode int,
) (json.RawMessage, bool, error) {
	if statusCode != http.StatusUnauthorized || retried || c.config.Auth.Type != "session" ||
		c.config.Auth.Username == "" || c.config.Auth.Password == "" {
		return nil, false, nil
	}

	session, csrf, err := loginWithPassword(ctx, c.base, c.config.Auth.Username, c.config.Auth.Password)
	if err != nil {
		return nil, false, nil
	}
	c.config.Auth.SessionToken = session
	c.config.Auth.CSRFToken = csrf
	if err := saveConfig(c.config); err != nil {
		// Best-effort: the refreshed session/CSRF token still works for the
		// rest of this run, it just won't be persisted to disk for next time.
		slog.Warn("cli: failed to persist refreshed session config", "error", err)
	}
	result, err := c.doRetry(ctx, method, path, body, true)
	return result, true, err
}
