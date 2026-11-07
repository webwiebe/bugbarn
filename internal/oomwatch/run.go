// Package oomwatch reports OOMKilled containers to BugBarn. It lists the pods
// of the cluster it runs in and sends one event per OOMKilled termination, so
// the kill shows up as an issue instead of only in `kubectl get pod`.
package oomwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultInterval = 30 * time.Second
	defaultLookback = 10 * time.Minute
	defaultProject  = "cluster"
)

// config comes from OOMWATCH_* variables:
//
//	OOMWATCH_ENDPOINT         events URL, for example https://bugbarn.example/api/v1/events (required)
//	OOMWATCH_API_KEY          ingest key (required); naming several projects needs a full-scope key
//	OOMWATCH_CLUSTER          cluster name carried on every event
//	OOMWATCH_DEFAULT_PROJECT  project for namespaces with no mapping (default "cluster")
//	OOMWATCH_PROJECTS         namespace=project pairs, comma separated
//	OOMWATCH_INTERVAL         scan interval as a Go duration (default 30s)
//	OOMWATCH_KUBE_API         API base URL, for tests; default is the in-cluster service
type config struct {
	endpoint, apiKey, cluster, defaultProject, kubeAPI string
	projects                                           map[string]string
	interval                                           time.Duration
}

func loadConfig(get func(string) string) (config, error) {
	c := config{
		endpoint:       get("OOMWATCH_ENDPOINT"),
		apiKey:         get("OOMWATCH_API_KEY"),
		cluster:        get("OOMWATCH_CLUSTER"),
		defaultProject: get("OOMWATCH_DEFAULT_PROJECT"),
		kubeAPI:        get("OOMWATCH_KUBE_API"),
		projects:       parseProjects(get("OOMWATCH_PROJECTS")),
		interval:       defaultInterval,
	}
	if c.endpoint == "" || c.apiKey == "" {
		return c, errors.New("OOMWATCH_ENDPOINT and OOMWATCH_API_KEY are required")
	}
	if c.defaultProject == "" {
		c.defaultProject = defaultProject
	}
	if raw := get("OOMWATCH_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Second {
			return c, fmt.Errorf("OOMWATCH_INTERVAL %q is not a duration of at least 1s", raw)
		}
		c.interval = d
	}
	return c, nil
}

func parseProjects(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		ns, project, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && ns != "" && project != "" {
			out[strings.TrimSpace(ns)] = strings.TrimSpace(project)
		}
	}
	return out
}

// Run scans until ctx ends. A failed scan or report is logged and retried on
// the next tick; only a bad configuration stops it.
func Run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	kube, err := newInClusterClient(cfg.kubeAPI)
	if err != nil {
		return err
	}
	rep := &reporter{
		endpoint: cfg.endpoint, apiKey: cfg.apiKey, cluster: cfg.cluster,
		defaultProject: cfg.defaultProject, projects: cfg.projects,
		http: &http.Client{Timeout: 15 * time.Second},
	}
	logger.Info("oomwatch started", "cluster", cfg.cluster, "interval", cfg.interval.String(), "mapped_namespaces", len(cfg.projects))
	return loop(ctx, logger, kube, rep, cfg.interval, newTracker(time.Now(), defaultLookback))
}

func loop(ctx context.Context, logger *slog.Logger, kube *kubeClient, rep *reporter, every time.Duration, tr *tracker) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		scan(ctx, logger, kube, rep, tr)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func scan(ctx context.Context, logger *slog.Logger, kube *kubeClient, rep *reporter, tr *tracker) {
	pods, err := kube.listPods(ctx)
	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("oomwatch: list pods failed", "error", err)
		}
		return
	}
	for _, k := range tr.fresh(kills(pods)) {
		if err := rep.report(ctx, k); err != nil {
			tr.unmark(k)
			if ctx.Err() == nil {
				logger.Warn("oomwatch: report failed, retrying next scan", "namespace", k.Namespace, "container", k.Container, "error", err)
			}
			continue
		}
		logger.Info("oomwatch: reported OOMKilled", "namespace", k.Namespace, "pod", k.Pod, "container", k.Container)
	}
}
