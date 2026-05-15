package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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
	return c.doRetry(method, path, body, false)
}

func (c *Client) doRetry(method, path string, body any, retried bool) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.base+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
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

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 401 && !retried && c.config.Auth.Type == "session" && c.config.Auth.Username != "" && c.config.Auth.Password != "" {
		session, csrf, err := loginWithPassword(c.base, c.config.Auth.Username, c.config.Auth.Password)
		if err == nil {
			c.config.Auth.SessionToken = session
			c.config.Auth.CSRFToken = csrf
			_ = saveConfig(c.config)
			return c.doRetry(method, path, body, true)
		}
	}

	if resp.StatusCode >= 400 {
		msg := string(respBody)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(msg))
	}

	return json.RawMessage(respBody), nil
}
