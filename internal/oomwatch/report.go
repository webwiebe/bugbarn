package oomwatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// reporter sends one BugBarn event per OOMKilled termination.
type reporter struct {
	endpoint       string
	apiKey         string
	cluster        string
	defaultProject string
	projects       map[string]string
	http           *http.Client
}

// project is the BugBarn project of a namespace: its mapped app, or the
// default cluster project.
func (r *reporter) project(namespace string) string {
	if p, ok := r.projects[namespace]; ok && p != "" {
		return p
	}
	return r.defaultProject
}

// payload is the event for one kill. The fingerprint is namespace plus
// container, so repeated kills stack on one issue whatever the pod name.
func (r *reporter) payload(k kill) map[string]any {
	limit := k.MemoryLimit
	if limit == "" {
		limit = "none set"
	}
	return map[string]any{
		"timestamp":    k.FinishedAt.Format(time.RFC3339),
		"severityText": "error",
		"body":         fmt.Sprintf("OOMKilled: %s/%s in pod %s", k.Namespace, k.Container, k.Pod),
		"exception": map[string]any{
			"type":    "OOMKilled",
			"message": fmt.Sprintf("%s/%s exceeded its memory limit (%s)", k.Namespace, k.Container, limit),
		},
		"fingerprint": "oomkilled:" + k.Namespace + "/" + k.Container,
		"attributes": map[string]any{
			"k8s.cluster.name":    r.cluster,
			"k8s.namespace.name":  k.Namespace,
			"k8s.pod.name":        k.Pod,
			"k8s.container.name":  k.Container,
			"k8s.memory.limit":    k.MemoryLimit,
			"k8s.restart.count":   k.RestartCount,
			"k8s.exit.code":       k.ExitCode,
			"k8s.terminated.time": k.FinishedAt.Format(time.RFC3339),
		},
		"sender": map[string]any{"sdk": map[string]any{"name": "bugbarn-oomwatch"}},
	}
}

func (r *reporter) report(ctx context.Context, k kill) error {
	body, err := json.Marshal(r.payload(k))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BugBarn-Api-Key", r.apiKey)
	req.Header.Set("X-BugBarn-Project", r.project(k.Namespace))
	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("send event: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 == 2 {
		return nil
	}
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	return fmt.Errorf("ingest answered HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(detail)))
}
