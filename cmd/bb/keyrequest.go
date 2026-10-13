package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// cmdKeyRequest asks an admin for an ingest key for an existing project and
// waits for the approval. It needs no login: the request and the poll are
// public, and the admin approves in the dashboard.
func cmdKeyRequest(args []string) error {
	fs := flag.NewFlagSet("key-request", flag.ContinueOnError)
	label := fs.String("label", "", "who is asking, shown to the admin (for example my-org/my-repo production)")
	baseURL := fs.String("url", "", "BugBarn URL (defaults to the one in the CLI config)")
	outputSecret := fs.Bool("output-secret", false, "print the approved key to stdout (once); pipe it, for example into gh secret set")
	timeout := fs.Duration("timeout", 30*time.Minute, "how long to wait for the approval")
	interval := fs.Duration("interval", 5*time.Second, "poll interval")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: bb key-request <project-slug> --label <who> --output-secret | <consumer>")
	}
	if *label == "" {
		return errors.New("--label is required (for example the repo and the environment)")
	}
	if !*outputSecret {
		// The key is handed out on one poll only. Waiting without a place to put
		// it would claim the key and lose it.
		return errors.New("--output-secret is required: the key is shown once, so pipe it, " +
			"for example: bb key-request <slug> --label X --output-secret | <consumer>")
	}
	base := strings.TrimRight(*baseURL, "/")
	if base == "" {
		cfg, err := loadConfig()
		if err != nil || cfg.URL == "" {
			return errors.New("no BugBarn URL: pass --url or run bb login first")
		}
		base = strings.TrimRight(cfg.URL, "/")
	}
	return runKeyRequest(keyRequestRun{
		base: base, slug: fs.Arg(0), label: *label,
		out: os.Stdout, info: os.Stderr,
		interval: *interval, timeout: *timeout,
		http: &http.Client{Timeout: 30 * time.Second},
	})
}

// reorderFlags moves the positional slug behind the flags so that both
// `bb key-request slug --label x` and `bb key-request --label x slug` parse.
func reorderFlags(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && a != "--output-secret" && a != "-output-secret" && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

type keyRequestRun struct {
	base, slug, label string
	out, info         io.Writer
	interval, timeout time.Duration
	http              *http.Client
}

type createdKeyRequest struct {
	ID         string `json:"id"`
	PollSecret string `json:"pollSecret"`
	ExpiresAt  string `json:"expiresAt"`
}

func (r keyRequestRun) create() (createdKeyRequest, error) {
	var created createdKeyRequest
	body, _ := json.Marshal(map[string]string{"label": r.label})
	resp, err := r.http.Post(r.base+"/api/v1/setup/"+r.slug+"/key-requests", "application/json", bytes.NewReader(body))
	if err != nil {
		return created, fmt.Errorf("create key request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusAccepted {
		return created, fmt.Errorf("create key request: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, &created); err != nil || created.ID == "" || created.PollSecret == "" {
		return created, errors.New("create key request: unexpected response")
	}
	return created, nil
}

// outcome maps a terminal poll status to the command's result. done is false
// while the request is still pending.
func (r keyRequestRun) outcome(status, key string) (done bool, err error) {
	switch status {
	case "approved":
		if key == "" {
			return true, errors.New("approved, but no key in the response (already claimed?)")
		}
		_, _ = fmt.Fprintln(r.out, key)
		_, _ = fmt.Fprintln(r.info, "Approved. The key was written to stdout and is not shown again.")
		return true, nil
	case "denied":
		return true, errors.New("the key request was denied")
	case "expired":
		return true, errors.New("the key request expired before it was approved")
	case "claimed":
		return true, errors.New("the key was already claimed")
	}
	return false, nil
}

func runKeyRequest(r keyRequestRun) error {
	created, err := r.create()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(r.info, "Key request %s created for %q. Waiting for an admin to approve it in the dashboard (expires %s).\n",
		created.ID, r.slug, created.ExpiresAt)

	deadline := time.Now().Add(r.timeout)
	for {
		status, key, err := r.poll(created.ID, created.PollSecret)
		if err != nil {
			return err
		}
		if done, err := r.outcome(status, key); done {
			return err
		}
		if time.Now().Add(r.interval).After(deadline) {
			return fmt.Errorf("gave up waiting after %s; request %s stays open until it expires", r.timeout, created.ID)
		}
		time.Sleep(r.interval)
	}
}

func (r keyRequestRun) poll(id, secret string) (status, key string, err error) {
	req, err := http.NewRequest(http.MethodGet, r.base+"/api/v1/setup/"+r.slug+"/key-requests/"+id, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("X-BugBarn-Poll-Secret", secret)
	resp, err := r.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("poll key request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusGone {
		return "", "", fmt.Errorf("poll key request: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		Status string `json:"status"`
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", errors.New("poll key request: unexpected response")
	}
	return out.Status, out.APIKey, nil
}
